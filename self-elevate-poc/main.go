//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

func isElevated() bool {
	token := windows.Token(0)
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()

	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}

	member, err := token.IsMember(adminSID)
	if err != nil {
		return false
	}
	return member
}

func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = windows.FullPath(exe)
	if err != nil {
		return err
	}

	verb := windows.StringToUTF16Ptr("runas")
	path := windows.StringToUTF16Ptr(exe)

	args := strings.Join(os.Args[1:], " ")
	var argPtr *uint16
	if len(args) > 0 {
		argPtr = windows.StringToUTF16Ptr(args)
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	var cwdPtr *uint16
	if cwd != "" {
		cwdPtr = windows.StringToUTF16Ptr(cwd)
	}

	if err := windows.ShellExecute(0, verb, path, argPtr, cwdPtr, windows.SW_NORMAL); err != nil {
		return err
	}

	return nil
}

func ensureServiceInstalled(exePath string) error {
	const (
		serviceName = "SelfElevatePoC"
		displayName = "Self Elevation Proof-of-Concept"
		description = "Proof-of-concept service created by the self-elevation demo."
	)

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	svc, err := m.OpenService(serviceName)
	if err == nil {
		defer svc.Close()
		fmt.Println("Service already installed. Nothing to do.")
		return nil
	}
	if !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return fmt.Errorf("open existing service: %w", err)
	}

	svc, err = m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName: displayName,
		Description: description,
		StartType:   mgr.StartManual,
	})
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer svc.Close()

	fmt.Println("Service installed successfully.")
	fmt.Printf("Service Name: %s\n", serviceName)
	fmt.Printf("Binary Path: %s\n", exePath)
	fmt.Println("The service is installed as Manual start and is not started automatically in this demo.")
	return nil
}

func main() {
	if isElevated() {
		fmt.Println("Process is running with administrative privileges.")

		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to determine executable path: %v\n", err)
			os.Exit(1)
		}
		exe, err = windows.FullPath(exe)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to resolve full executable path: %v\n", err)
			os.Exit(1)
		}

		if err := ensureServiceInstalled(exe); err != nil {
			fmt.Fprintf(os.Stderr, "service installation failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Println("Process is not elevated. Relaunching with elevation...")
	if err := relaunchElevated(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to relaunch elevated: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Elevation request sent. The elevated instance will continue the installation.")
}
