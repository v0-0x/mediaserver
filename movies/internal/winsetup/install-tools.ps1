# KumoBox setup for Windows: installs the programs KumoBox uses, for this Windows
# user (no administrator rights), with Scoop (https://scoop.sh):
#
#   ffmpeg   ffmpeg and ffprobe: the in-app player (most files), stream
#            downloads and reading the tracks of video files
#   mpv      the external player (Scoop's extras bucket)
#   yt-dlp   faster, more reliable stream downloads
#   git      only because Scoop needs it to add its extras bucket
#   gh       the GitHub CLI, only if KumoBox's repository is private: signed in
#            to GitHub, KumoBox can then check for and download its updates
#
# Programs that are already installed, with Scoop or otherwise, are left
# alone, so it's safe to run again. The KumoBox installer offers to run it, and
# KumoBox runs it from Settings > Desktop app & programs.
#
# Keep this file ASCII: Windows PowerShell reads a script without a byte
# order mark in the computer's code page.

param(
    # The GitHub repository KumoBox updates from.
    [string]$Repo = "v0-0x/kumobox-movies",
    # The same repository by its number, as KumoBox's updater asks for it: it
    # stays the same when the repository is renamed or moves.
    [string]$RepoID = "1411506405",
    # Close the window at the end without waiting for Enter.
    [switch]$NoPause
)

# Scoop's own errors are reported by checking what got installed, not by
# stopping at the first one.
$ErrorActionPreference = "Continue"
# Windows PowerShell's download progress bar makes downloads very slow.
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
try { $Host.UI.RawUI.WindowTitle = "KumoBox setup" } catch {}

$script:failed = @()
$scoopDir = if ($env:SCOOP) { $env:SCOOP } else { Join-Path $env:USERPROFILE "scoop" }

function Step($text) {
    Write-Host ""
    Write-Host "==> $text" -ForegroundColor Cyan
}

function Have($command) {
    return [bool](Get-Command $command -ErrorAction SilentlyContinue)
}

function Installed($app) {
    return Test-Path (Join-Path $scoopDir "apps\$app\current")
}

# Install installs a Scoop app, unless it's installed already: with Scoop,
# or (when command is given) anywhere on the PATH.
function Install($app, $command, $why) {
    if ((Installed $app) -or ($command -and (Have $command))) {
        Write-Host "$app is installed" -ForegroundColor DarkGray
        return
    }
    Step "Installing $app ($why)"
    scoop install $app
    if (-not (Installed $app)) {
        $script:failed += $app
    }
}

Write-Host "KumoBox setup: installs the programs KumoBox uses with Scoop, for your Windows user." -ForegroundColor Cyan
Write-Host "It takes a few minutes. Programs you already have are skipped."

try {
    if (-not (Have "scoop")) {
        Step "Installing Scoop"
        # Scoop needs PowerShell scripts allowed for this user; Windows
        # blocks them by default and RemoteSigned is what Scoop asks for.
        $policy = Get-ExecutionPolicy -Scope CurrentUser
        if ($policy -eq "Undefined" -or $policy -eq "Restricted") {
            try { Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser -Force -ErrorAction Stop } catch { Write-Host $_ -ForegroundColor Yellow }
        }
        # Scoop's installer, run as a script of its own: when it gives up it
        # ends its own PowerShell, not this one.
        $installer = Join-Path $env:TEMP "kumobox-install-scoop.ps1"
        Invoke-RestMethod -Uri "https://get.scoop.sh" -OutFile $installer
        $principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
        $admin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
        $installerArgs = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $installer)
        if ($admin) { $installerArgs += "-RunAsAdmin" }
        & powershell.exe @installerArgs
        Remove-Item $installer -ErrorAction SilentlyContinue
        $env:PATH = (Join-Path $scoopDir "shims") + ";" + $env:PATH
        if (-not (Have "scoop")) {
            throw "Scoop didn't install (see above)."
        }
    }

    # Scoop needs git to add buckets: it comes first.
    Install "git" "git" "Scoop adds its extras bucket with it"
    if (-not (Test-Path (Join-Path $scoopDir "buckets\extras"))) {
        Step "Adding Scoop's extras bucket (mpv is in it)"
        scoop bucket add extras
        if (-not (Test-Path (Join-Path $scoopDir "buckets\extras"))) {
            $script:failed += "Scoop's extras bucket"
        }
    }
    Install "ffmpeg" "ffmpeg" "the in-app player and downloads"
    Install "yt-dlp" "yt-dlp" "stream downloads"
    Install "mpv" "mpv" "the external player"

    # KumoBox checks GitHub for its updates. Were its repository private, that
    # would take a GitHub sign-in, which the GitHub CLI keeps.
    $public = $true
    try {
        Invoke-RestMethod -Uri "https://api.github.com/repositories/$RepoID" -Headers @{ "User-Agent" = "KumoBox-setup" } | Out-Null
    } catch {
        $public = $false
    }
    if (-not $public) {
        Install "gh" "gh" "signs KumoBox in to GitHub for its updates"
        if (Have "gh") {
            gh auth status --hostname github.com *> $null
            if ($LASTEXITCODE -ne 0) {
                Step "Signing in to GitHub"
                Write-Host "KumoBox checks GitHub for its updates, and its repository is private:"
                Write-Host "sign in with the GitHub account that can see $Repo."
                $answer = Read-Host "Sign in now? [Y/n]"
                if ($answer -notmatch "^\s*[nN]") {
                    gh auth login --hostname github.com --git-protocol https --web
                    if ($LASTEXITCODE -ne 0) {
                        $script:failed += "the GitHub sign-in"
                    }
                }
            }
        }
    }
} catch {
    Write-Host ""
    Write-Host "KumoBox setup stopped: $_" -ForegroundColor Red
    $script:failed += "the rest"
}

Write-Host ""
if ($script:failed.Count -eq 0) {
    Write-Host "All done. KumoBox finds the new programs by itself." -ForegroundColor Green
} else {
    Write-Host ("Not installed: " + ($script:failed -join ", ") + ".") -ForegroundColor Yellow
    Write-Host "Run the setup again from KumoBox (Settings > Desktop app & programs), or see"
    Write-Host "https://github.com/$Repo/blob/HEAD/packaging/windows/README.md"
}
if (-not $NoPause) {
    Read-Host "Press Enter to close this window" | Out-Null
}
exit $script:failed.Count
