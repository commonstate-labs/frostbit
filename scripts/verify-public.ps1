  
         
                                                                      
  
$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    function Invoke-Checked([string]$Name, [scriptblock]$Body) {
        Write-Host "==> $Name" -ForegroundColor Cyan
        & $Body
        if ($LASTEXITCODE -ne 0) { throw "$Name failed with exit code $LASTEXITCODE" }
    }

    $goPackages = @(& go list -buildvcs=false ./... | Where-Object { $_ -and $_ -ne "dex/tmp" })
    if ($LASTEXITCODE -ne 0 -or $goPackages.Count -eq 0) { throw "failed to enumerate Go packages" }
    $goBuildPackages = @(& go list -buildvcs=false -f '{{if or .GoFiles .CgoFiles}}{{.ImportPath}}{{end}}' ./... | Where-Object { $_ -and $_ -ne "dex/tmp" })
    if ($LASTEXITCODE -ne 0 -or $goBuildPackages.Count -eq 0) { throw "failed to enumerate buildable Go packages" }

    Write-Host "==> public config credential-field check" -ForegroundColor Cyan
    $fieldPattern = '"[^"\r\n]*(private[_-]?key|privateKey|mnemonic|seed[_-]?phrase|password|api[_-]?token|access[_-]?token)[^"\r\n]*"\s*:'
    $configFiles = @(Get-ChildItem -LiteralPath config, deploy -Recurse -File | Where-Object { $_.Extension -in ".json", ".yml", ".yaml" })
    $fieldMatches = @($configFiles | Select-String -Pattern $fieldPattern -CaseSensitive:$false)
    if ($fieldMatches.Count -ne 0) { throw "public config contains a secret-bearing field name: $($fieldMatches[0].Path):$($fieldMatches[0].LineNumber)" }
    $secretFiles = @(Get-ChildItem -LiteralPath config, deploy -Recurse -File | Where-Object { $_.Extension -in ".key", ".pem", ".p12", ".pfx", ".keystore" })
    if ($secretFiles.Count -ne 0) { throw "public config contains a secret-bearing file type: $($secretFiles[0].FullName)" }

    Invoke-Checked "go build" { & go build -buildvcs=false $goBuildPackages }
    Invoke-Checked "go test" { & go test -buildvcs=false $goPackages -count=1 -short }
    Invoke-Checked "key registry safety-net" { & go test -buildvcs=false ./keys/... -run Registry -count=1 }
    Invoke-Checked "container and release contracts" { & go test -buildvcs=false ./deploy ./cmd/release -count=1 }
    Invoke-Checked "go vet" { & go vet -buildvcs=false $goPackages }

    Write-Host "==> safe-math lint" -ForegroundColor Cyan
    $safeMathPattern = 'new\(big\.Int\)\.(Add|Sub|Mul)|\.(Add|Sub|Mul)\(.*big\.Int'
    $safeMathViolations = @(Get-ChildItem -LiteralPath vm, future -Recurse -Filter *.go -File |
        Where-Object { $_.Name -ne "safe_math.go" -and $_.Name -notlike "*_test.go" } |
        Select-String -Pattern $safeMathPattern |
        Where-Object { $_.Line -notmatch '//\s*safe-math-ok' })
    if ($safeMathViolations.Count -ne 0) { throw "unsafe bare big.Int operation: $($safeMathViolations[0].Path):$($safeMathViolations[0].LineNumber)" }

    Invoke-Checked "deterministic node and explorer build" { & scripts/build.ps1 -Tag verify }

    Invoke-Checked "explorer npm ci" { & npm --prefix explorer ci --no-audit --no-fund }
    Invoke-Checked "explorer test" { & npm --prefix explorer test }
    Invoke-Checked "explorer build" { & npm --prefix explorer run build }
    Invoke-Checked "wallet npm ci" { & npm --prefix wallet ci --no-audit --no-fund }
    Invoke-Checked "wallet test" { & npm --prefix wallet test }
    Invoke-Checked "wallet build" { & npm --prefix wallet run build }

    Write-Host "==> generated protobuf check" -ForegroundColor Cyan
    $generatedProto = Join-Path ([IO.Path]::GetTempPath()) ("dex-data-proto-" + [Guid]::NewGuid().ToString("N") + ".js")
    try {
        Push-Location wallet
        try { Invoke-Checked "wallet protobuf generation" { & npx --no-install pbjs -t static-module -w es6 --es6 -o $generatedProto ../pb/data.proto } } finally { Pop-Location }
        if ((Get-FileHash -Algorithm SHA256 -LiteralPath $generatedProto).Hash -ne (Get-FileHash -Algorithm SHA256 -LiteralPath wallet/lib/data_proto.js).Hash) {
            throw "wallet/lib/data_proto.js is stale; regenerate it from pb/data.proto"
        }
    } finally {
        Remove-Item -Force -LiteralPath $generatedProto -ErrorAction SilentlyContinue
    }
    if (-not (Select-String -LiteralPath pb/data.pb.go -SimpleMatch '// source: pb/data.proto' -Quiet)) { throw "pb/data.pb.go is not the generated output for pb/data.proto" }

    Write-Host "==> public verification passed" -ForegroundColor Green
} finally {
    Pop-Location
}
