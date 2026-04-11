@echo off
REM Generate mocks for Trace Forge
REM This script uses mockgen (golang/mock) to generate interface mocks
REM Mocks are used for unit testing without requiring a live Cassandra instance

setlocal enabledelayedexpansion

echo Generating mocks...

echo   - Generating Repository mock...
go run github.com/golang/mock/cmd/mockgen@latest ^
  -source=internal/storage/repository.go ^
  -destination=internal/storage/mock_repository.go ^
  -package=storage

if errorlevel 1 (
    echo ❌ Mock generation failed
    exit /b 1
)

echo ✅ Mocks generated successfully
echo    - internal/storage/mock_repository.go

endlocal
