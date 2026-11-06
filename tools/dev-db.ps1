<#
.SYNOPSIS
  Local PostgreSQL helper for OpenBekci development (Windows, user-process Postgres).
.EXAMPLE
  tools/dev-db.ps1 init; tools/dev-db.ps1 start; tools/dev-db.ps1 roles
#>
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('init', 'start', 'stop', 'roles', 'url')]
    [string]$Command
)

$ErrorActionPreference = 'Stop'

$PgBin   = if ($env:PG_BIN) { $env:PG_BIN } else { 'C:\Program Files\PostgreSQL\17\bin' }
$Root    = if ($env:OPENBEKCI_DEV_DIR) { $env:OPENBEKCI_DEV_DIR } else { Join-Path $env:LOCALAPPDATA 'OpenBekci' }
$DataDir = Join-Path $Root 'pgdata'
$LogFile = Join-Path $Root 'pg.log'
$Port    = if ($env:PGPORT) { $env:PGPORT } else { '5432' }
$RolesSql = Join-Path $PSScriptRoot '..\deploy\compose\init\01-roles.sql'

function Test-Ready {
    & "$PgBin\pg_isready.exe" -h localhost -p $Port -q
    return ($LASTEXITCODE -eq 0)
}

switch ($Command) {
    'init' {
        if (Test-Path (Join-Path $DataDir 'PG_VERSION')) { Write-Host "Data dir exists: $DataDir"; break }
        New-Item -ItemType Directory -Force $Root | Out-Null
        $pw = New-TemporaryFile
        Set-Content -Path $pw -Value 'postgres' -NoNewline
        try {
            & "$PgBin\initdb.exe" -D $DataDir -U postgres --pwfile=$pw --auth=scram-sha-256 -E UTF8 --locale=C
        } finally { Remove-Item $pw -Force }
        if ($LASTEXITCODE -ne 0) { throw 'initdb failed' }
    }
    'start' {
        if (Test-Ready) { Write-Host 'Already running.'; break }
        # No -w: pg_ctl -w hangs under some shell wrappers. Poll instead.
        & "$PgBin\pg_ctl.exe" start -D $DataDir -l $LogFile -o "-p $Port"
        for ($i = 0; $i -lt 60; $i++) {
            if (Test-Ready) { Write-Host 'PostgreSQL is ready.'; return }
            Start-Sleep -Milliseconds 500
        }
        throw "PostgreSQL did not become ready; see $LogFile"
    }
    'stop' {
        & "$PgBin\pg_ctl.exe" stop -D $DataDir -m fast
    }
    'roles' {
        $env:PGPASSWORD = if ($env:PGSUPERPASSWORD) { $env:PGSUPERPASSWORD } else { 'postgres' }
        & "$PgBin\psql.exe" -h localhost -p $Port -U postgres -d postgres -v ON_ERROR_STOP=1 -f $RolesSql
        if ($LASTEXITCODE -ne 0) { throw 'roles script failed' }
    }
    'url' {
        foreach ($r in 'app', 'platform', 'migrator') {
            $name = if ($r -eq 'app') { 'OPENBEKCI_DATABASE_URL' } else { "OPENBEKCI_$($r.ToUpper())_DATABASE_URL" }
            Write-Host "$name=postgres://openbekci_${r}:openbekci@localhost:$Port/openbekci"
        }
    }
}
