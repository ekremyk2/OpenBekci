# Runs the pinned go-task (see server/go.mod `tool` directives); no global install needed.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Push-Location (Join-Path $root 'server')
try {
    go tool task --taskfile (Join-Path $root 'Taskfile.yml') --dir $root @args
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
