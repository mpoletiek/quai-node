# Install a Quai Network node with quai-node on Windows, from its GitHub
# releases. In PowerShell, as administrator (right-click, Run as
# administrator) so the node can run as a Windows service:
#
#   irm https://raw.githubusercontent.com/mpoletiek/quai-node/main/install.ps1 | iex
#
# It downloads quai-node for Windows, checks it against the release's
# SHA256SUMS, and runs `quai-node setup`, which asks a few questions and
# sets the node up in %ProgramData%\quai-node, with `quai-node` on your
# PATH. Without administrator rights setup still works, but the node runs
# in the foreground (`quai-node run`) instead of as a service. Run it again
# to update; setup keeps your earlier answers.
#
# Settings, as environment variables:
#   QUAI_NODE_VERSION  a release tag to install (default: the newest release)
#   QUAI_NODE_ARGS     arguments for `quai-node setup`, e.g. "--apply" to
#                      update without questions

& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # the progress bar slows downloads a lot
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = 'mpoletiek/quai-node'
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
    if ($arch -ne 'AMD64') {
        Write-Error "install: there is no Windows $arch build (only x86-64)"
        return
    }

    $version = $env:QUAI_NODE_VERSION
    if (-not $version) {
        try {
            $version = (Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$repo/releases/latest").tag_name
        } catch {
            Write-Error "install: could not ask GitHub for the newest release (rate limited or offline?); set QUAI_NODE_VERSION to a tag, e.g. v0.1.0"
            return
        }
    }
    if (-not $version.StartsWith('v')) { $version = "v$version" }

    $asset = "quai-node-$version-windows-amd64.exe"
    $base = "https://github.com/$repo/releases/download/$version"
    $work = Join-Path ([IO.Path]::GetTempPath()) ("quai-node-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $work | Out-Null
    try {
        Write-Host "Downloading quai-node $version (windows-amd64)"
        $exe = Join-Path $work 'quai-node.exe'
        try {
            Invoke-WebRequest -UseBasicParsing "$base/$asset" -OutFile $exe
        } catch {
            Write-Error "install: no windows-amd64 build in release $version ($base/$asset)"
            return
        }
        try {
            $sums = (Invoke-WebRequest -UseBasicParsing "$base/SHA256SUMS").Content
        } catch {
            Write-Error "install: release $version has no SHA256SUMS; not running an unchecked download"
            return
        }
        if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
        $expected = $null
        foreach ($line in ($sums -split "`n")) {
            $f = $line.Trim() -split '\s+'
            if ($f.Count -eq 2 -and ($f[1] -eq $asset -or $f[1] -eq "*$asset")) { $expected = $f[0].ToLower() }
        }
        if (-not $expected) {
            Write-Error "install: SHA256SUMS does not list $asset"
            return
        }
        $actual = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
        if ($actual -ne $expected) {
            Write-Error "install: checksum mismatch for $asset (expected $expected, got $actual); nothing was installed"
            return
        }
        Write-Host 'Checksum verified.'

        $admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
        if (-not $admin) {
            Write-Host ''
            Write-Host 'Not running as administrator: the node can be set up, but not as a Windows service.'
            Write-Host 'To run it in the background, open PowerShell as administrator and run this again.'
        }
        Write-Host ''
        # Setup copies itself into %ProgramData%\quai-node, so the download can go.
        $setupArgs = @('setup')
        if ($env:QUAI_NODE_ARGS) { $setupArgs += ($env:QUAI_NODE_ARGS -split '\s+' | Where-Object { $_ }) }
        & $exe @setupArgs
        if ($LASTEXITCODE -ne 0) { Write-Error "install: quai-node setup exited with code $LASTEXITCODE" }

        # Setup put its bin directory on the user's PATH in the registry; this
        # window started before that, so reread PATH here too.
        $bin = Join-Path $env:ProgramData 'quai-node\bin'
        $env:Path = (@([Environment]::GetEnvironmentVariable('Path', 'Machine'),
                [Environment]::GetEnvironmentVariable('Path', 'User')) | Where-Object { $_ }) -join ';'
        Write-Host ''
        if (Get-Command quai-node -ErrorAction SilentlyContinue) {
            Write-Host 'quai-node is on your PATH: try  quai-node status'
        } elseif (Test-Path (Join-Path $bin 'quai-node.exe')) {
            Write-Host "quai-node isn't on your PATH yet. To add it for your user, run:"
            Write-Host ''
            Write-Host "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';$bin', 'User')"
            Write-Host ''
            Write-Host "and open a new terminal (close every Windows Terminal window first). Or run it by its full path: & '$bin\quai-node.exe' status"
        }
    } finally {
        Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
    }
}
