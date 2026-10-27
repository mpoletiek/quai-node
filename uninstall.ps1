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
#
# If %ProgramData%\quai-node was already deleted by hand, quai-node went
# with it. Then this removes what it left outside that folder: the Windows
# service, and the folder on your PATH. It removes only those that point
# into a folder that no longer exists.

& {
    $ErrorActionPreference = 'Stop'
    $uninstallArgs = @()
    if ($env:QUAI_NODE_ARGS) { $uninstallArgs = @($env:QUAI_NODE_ARGS -split '\s+' | Where-Object { $_ }) }

    $installed = Join-Path $env:ProgramData 'quai-node\bin\quai-node.exe'
    if (-not (Test-Path $installed)) {
        $cmd = Get-Command quai-node -ErrorAction SilentlyContinue
        if (-not $cmd) {
            # quai-node is gone: find what it left behind. A service of
            # quai-node's (or one of the separate ones earlier releases
            # installed) whose program no longer exists:
            $services = @(foreach ($name in 'quai-node', 'quai-dash', 'go-quai') {
                $svc = Get-CimInstance Win32_Service -Filter "Name='$name'" -ErrorAction SilentlyContinue
                if ($svc -and $svc.PathName -match '^\s*"?([^"]*\\bin\\(quai-node|go-quai|quai-dash)\.exe)' -and
                    -not (Test-Path -LiteralPath $Matches[1])) { $name }
            })
            # and a quai-node bin folder on the user's PATH that no longer exists.
            $envKey = Get-Item 'HKCU:\Environment'
            $userPath = $envKey.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
            $entries = @("$userPath" -split ';')
            $stale = @($entries | Where-Object {
                $_ -and $_.TrimEnd('\') -like '*\quai-node\bin' -and
                -not (Test-Path -LiteralPath ([Environment]::ExpandEnvironmentVariables($_)))
            })
            if ($services.Count -eq 0 -and $stale.Count -eq 0) {
                Write-Error "uninstall: no quai-node in $installed or on your PATH, and nothing of it left behind; nothing to uninstall"
                return
            }

            Write-Host "quai-node is gone (its folder was deleted), but it left these behind:"
            foreach ($name in $services) { Write-Host "  the $name Windows service" }
            foreach ($dir in $stale) { Write-Host "  $dir, on your PATH" }
            Write-Host "Chain data kept outside that folder, if any, stays."
            if ($uninstallArgs -contains '--dry-run') { return }
            if (-not (($uninstallArgs -contains '--yes') -or ($uninstallArgs -contains '-y'))) {
                $answer = Read-Host 'Remove them? [y/N]'
                if ($answer -notmatch '^(y|yes)$') {
                    Write-Error "uninstall: nothing removed"
                    return
                }
            }
            if ($services.Count -gt 0) {
                $admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
                    [Security.Principal.WindowsBuiltInRole]::Administrator)
                if (-not $admin) {
                    Write-Error "uninstall: removing a Windows service needs an administrator: run PowerShell as administrator and try again"
                    return
                }
                foreach ($name in $services) {
                    & sc.exe stop $name *> $null
                    & sc.exe delete $name *> $null
                    if ($LASTEXITCODE -ne 0) { Write-Error "uninstall: couldn't remove the $name service (sc.exe delete exited with $LASTEXITCODE)" }
                }
            }
            if ($stale.Count -gt 0) {
                $kind = $envKey.GetValueKind('Path')
                $keep = @($entries | Where-Object { $_ -and $stale -notcontains $_ })
                Set-ItemProperty -Path 'HKCU:\Environment' -Name Path -Value ($keep -join ';') -Type $kind
            }
            Write-Host "Removed. Open a new terminal for the PATH to update."
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
        & $copy uninstall @uninstallArgs
        if ($LASTEXITCODE -ne 0) { Write-Error "uninstall: quai-node uninstall exited with code $LASTEXITCODE" }
    } finally {
        Remove-Item -Force $copy -ErrorAction SilentlyContinue
    }
}
