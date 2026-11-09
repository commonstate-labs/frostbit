  
         
                                                                                      
        
                                               
  
param([string]$Tag = "dev")

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$commit = (& git rev-parse --short HEAD 2>$null)
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($commit)) { $commit = "unknown" } else { $commit = $commit.Trim() }
$time = (& git show -s --format=%cI HEAD 2>$null)
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($time)) { $time = "unknown" } else { $time = $time.Trim() }

$ldflags = "-X main.BuildCommit=$commit -X main.BuildTime=$time -X main.BuildTag=$Tag"

New-Item -ItemType Directory -Force -Path "bin" | Out-Null

Write-Host "==> building node commit=$commit time=$time tag=$Tag"
& go build -trimpath -buildvcs=false -ldflags $ldflags -o bin/node.exe ./cmd/main
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> building explorer"
& go build -trimpath -buildvcs=false -ldflags $ldflags -o bin/explorer.exe ./cmd/explorer
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> building node-key"
& go build -trimpath -buildvcs=false -o bin/node-key.exe ./cmd/node-key
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> done: bin/node.exe bin/explorer.exe bin/node-key.exe"
