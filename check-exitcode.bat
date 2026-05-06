@echo off
setlocal

set "VAPS_EXE=%~dp0bin\vaps.exe"
if not exist "%VAPS_EXE%" (
  echo missing "%VAPS_EXE%" 1>&2
  exit /b 1
)

"%VAPS_EXE%" %*
set "VAPS_ERRORLEVEL=%ERRORLEVEL%"
echo ERRORLEVEL=%VAPS_ERRORLEVEL%
exit /b %VAPS_ERRORLEVEL%
