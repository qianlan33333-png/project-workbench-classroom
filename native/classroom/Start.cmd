@echo off
cd /d "%~dp0"
workbench-classroom.exe
if errorlevel 1 pause
