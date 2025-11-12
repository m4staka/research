# Self Elevation Proof-of-Concept

This proof-of-concept demonstrates how a Go-based bootstrapper can relaunch itself with elevated privileges on Windows using `ShellExecute` and the `runas` verb. Once elevated, it installs a placeholder Windows service pointing back to the executable so you can validate the privileged workflow end-to-end.

## Project layout

```
self-elevate-poc/
├── go.mod
├── go.sum
└── main.go
```

## How it works

1. The process checks whether it is already running with administrative privileges using Windows access tokens.
2. If elevated, it resolves its own absolute path and installs (or reuses) a manual-start Windows service named `SelfElevatePoC` using the Service Control Manager APIs.
3. If the service does not already exist, it is created with the binary path pointing back to the compiled executable. The demo does **not** attempt to start the service.
4. If not elevated, it calls `ShellExecute` with the `runas` verb, prompting the User Account Control (UAC) dialog to relaunch the same executable with elevation.

## Building

Build on a Windows machine with Go installed:

```powershell
# From the repository root
cd self-elevate-poc
go build -o SelfElevate.exe
```

## Running

Double-click the compiled `SelfElevate.exe` or run it from a console. On first run it should display:

```
Process is not elevated. Relaunching with elevation...
Elevation request sent. The elevated instance will continue the installation.
```

Windows will show a UAC consent dialog. After approving, the elevated instance prints something similar to:

```
Process is running with administrative privileges.
Service installed successfully.
Service Name: SelfElevatePoC
Binary Path: C:\path\to\SelfElevate.exe
The service is installed as Manual start and is not started automatically in this demo.
```

You can verify the service with PowerShell:

```powershell
Get-Service SelfElevatePoC | Format-List Name, Status, StartType, DisplayName
```

Re-running the executable after the service is installed reports that nothing needs to be done. To remove the service when you are finished evaluating the demo:

```powershell
sc.exe delete SelfElevatePoC
```
