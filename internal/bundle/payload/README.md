`scripts/build-payload.sh` writes `payload.tar.gz` here for one OS/arch
before `go build`. It is embedded into quai-node and is not committed.
Without it, quai-node builds and runs, but `setup` reports that this build
carries no node.
