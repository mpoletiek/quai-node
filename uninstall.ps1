# Uninstall quai-node on Windows. In PowerShell, as administrator if the
# node runs as a Windows service:
#
#   irm https://raw.githubusercontent.com/mpoletiek/quai-node/main/uninstall.ps1 | iex
#
# It runs `quai-node uninstall`, which stops and removes the quai-node
# service and removes go-quai, quai-dash and quai-node. The chain data, the
# node's key, its logs and settings stay, so a later install picks up where
# it left off.
#
# Settings, as environment variables:
#   QUAI_NODE_ARGS  arguments for `quai-node uninstall`: "--purge" also
#                   deletes the chain data, key, logs and settings

& {
    $ErrorActionPreference = 'Stop'
    $installed = Join-Path $env:ProgramData 'quai-node\bin\quai-node.exe'
    if (-not (Test-Path $installed)) {
        $cmd = Get-Command quai-node -ErrorAction SilentlyContinue
        if (-not $cmd) {
            Write-Error "uninstall: no quai-node in $installed or on your PATH; nothing to uninstall"
            return
        }
        $installed = $cmd.Source
    }
    # Only ever run a file that says it is quai-node.
    $version = (& $installed version 2>$null | Select-Object -First 1)
    if (-not ("$version" -like 'quai-node *')) {
        Write-Error "uninstall: $installed does not identify itself as quai-node; leaving it alone"
        return
    }

    # Windows can't delete a running program, and uninstall deletes the
    # folder quai-node runs from: run a copy from the temp folder instead.
    $copy = Join-Path ([IO.Path]::GetTempPath()) ("quai-node-uninstall-" + [Guid]::NewGuid().ToString('N') + '.exe')
    Copy-Item $installed $copy
    try {
        Write-Host "Uninstalling $version"
        $uninstallArgs = @('uninstall')
        if ($env:QUAI_NODE_ARGS) { $uninstallArgs += ($env:QUAI_NODE_ARGS -split '\s+' | Where-Object { $_ }) }
        & $copy @uninstallArgs
        if ($LASTEXITCODE -ne 0) { Write-Error "uninstall: quai-node uninstall exited with code $LASTEXITCODE" }
    } finally {
        Remove-Item -Force $copy -ErrorAction SilentlyContinue
    }
}
