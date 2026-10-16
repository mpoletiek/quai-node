package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// quikshot.dev's files come from Cloudflare R2: one stream gets about
// 60 MB/s, and a request waits 0.3-0.8 s for its first byte. So several
// requests run at once, each for a chunk: 16 of 16 MiB filled a gigabit
// line (110 MB/s) and leave room for faster ones. The archive still has to
// reach the hash and decoders in order, so Read hands the chunks out in
// turn, with at most window of them waiting (about 400 MiB in memory).
// Variables for tests.
var (
	connections        = 16
	chunkSize    int64 = 16 << 20
	window             = 24
	stallTimeout       = 60 * time.Second
	retryWait          = 5 * time.Second
)

var dlClient = &http.Client{} // no overall timeout: the download takes hours

type chunk struct {
	buf []byte
	err error
}

type job struct {
	off int64
	buf []byte
	res chan<- chunk
}

// parallelBody reads url from start to end over several connections. A
// chunk whose connection drops, or stalls, is fetched again from where it
// stopped: a download of hundreds of gigabytes will see some.
type parallelBody struct {
	ctx    context.Context
	cancel context.CancelFunc
	url    string
	total  int64
	count  *atomic.Int64 // bytes received so far, in any order

	order chan (<-chan chunk) // each chunk's result, in order
	free  chan []byte         // buffers to reuse
	cur   []byte              // what's left of the chunk being read
	held  []byte              // that chunk's buffer
	err   error
	wg    sync.WaitGroup
}

// download starts fetching url, total bytes long; count adds up the bytes
// received. Close stops it.
func download(ctx context.Context, url string, total int64, count *atomic.Int64) *parallelBody {
	ctx, cancel := context.WithCancel(ctx)
	b := &parallelBody{ctx: ctx, cancel: cancel, url: url, total: total, count: count,
		order: make(chan (<-chan chunk), window), free: make(chan []byte, window+2)}
	jobs := make(chan job)
	for range connections {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			for j := range jobs {
				j.res <- chunk{j.buf, b.fetch(j.off, j.buf)}
			}
		}()
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer close(b.order)
		defer close(jobs)
		for off := int64(0); off < total; off += chunkSize {
			res := make(chan chunk, 1)
			select { // blocks while window chunks wait to be read
			case b.order <- res:
			case <-ctx.Done():
				return
			}
			j := job{off, b.buffer(min(chunkSize, total-off)), res}
			select {
			case jobs <- j:
			case <-ctx.Done():
				return
			}
		}
	}()
	return b
}

func (b *parallelBody) buffer(n int64) []byte {
	select {
	case buf := <-b.free:
		return buf[:n]
	default:
		return make([]byte, n, chunkSize)
	}
}

func (b *parallelBody) Read(p []byte) (int, error) {
	for len(b.cur) == 0 {
		if b.err != nil {
			return 0, b.err
		}
		if b.held != nil {
			select {
			case b.free <- b.held:
			default:
			}
			b.held = nil
		}
		res, ok := <-b.order
		if !ok {
			b.err = io.EOF
			if err := b.ctx.Err(); err != nil {
				b.err = err
			}
			continue
		}
		select {
		case c := <-res:
			if c.err != nil {
				b.err = c.err
			} else {
				b.cur, b.held = c.buf, c.buf
			}
		case <-b.ctx.Done():
			b.err = b.ctx.Err()
		}
	}
	n := copy(p, b.cur)
	b.cur = b.cur[n:]
	return n, nil
}

func (b *parallelBody) Close() error {
	b.cancel()
	b.wg.Wait()
	return nil
}

// fetch fills buf from offset off, retrying from where a failed request
// stopped, giving up after 8 failures in a row.
func (b *parallelBody) fetch(off int64, buf []byte) error {
	got, fails := 0, 0
	for got < len(buf) {
		n, err := b.get(off+int64(got), buf[got:])
		got += n
		if err == nil {
			continue
		}
		if b.ctx.Err() != nil {
			return b.ctx.Err()
		}
		if n > 0 {
			fails = 0
		}
		if fails++; fails > 8 {
			return fmt.Errorf("download stopped at byte %d of %d: %w", off+int64(got), b.total, err)
		}
		select {
		case <-time.After(time.Duration(fails) * retryWait):
		case <-b.ctx.Done():
			return b.ctx.Err()
		}
	}
	return nil
}

var errStalled = errors.New("no data for a minute")

// get reads bytes off… of url into buf with one Range request, and how
// many it got.
func (b *parallelBody) get(off int64, buf []byte) (int, error) {
	ctx, cancel := context.WithCancel(b.ctx)
	defer cancel()
	stall := time.AfterFunc(stallTimeout, cancel)
	defer stall.Stop()
	stalled := func(err error) error {
		if b.ctx.Err() == nil && ctx.Err() != nil {
			return errStalled
		}
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.url, nil)
	if err != nil {
		return 0, err
	}
	end := off + int64(len(buf)) - 1
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, end))
	resp, err := dlClient.Do(req)
	if err != nil {
		return 0, stalled(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("snapshot download: HTTP %d", resp.StatusCode)
	}
	if want := fmt.Sprintf("bytes %d-%d/", off, end); !strings.HasPrefix(resp.Header.Get("Content-Range"), want) {
		return 0, fmt.Errorf("snapshot download: asked for %s, got %q", want, resp.Header.Get("Content-Range"))
	}
	got := 0
	for got < len(buf) {
		n, err := resp.Body.Read(buf[got:])
		got += n
		b.count.Add(int64(n))
		stall.Reset(stallTimeout)
		if err == io.EOF && got < len(buf) {
			err = io.ErrUnexpectedEOF
		}
		if err != nil && err != io.EOF {
			return got, stalled(err)
		}
	}
	return got, nil
}
