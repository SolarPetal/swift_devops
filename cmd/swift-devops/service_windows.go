//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/svc"
)

const windowsServiceName = "swift-devops"

func runService(args []string) {
	cfgPath := parseConfigPath(args)
	if err := svc.Run(windowsServiceName, &windowsService{configPath: cfgPath}); err != nil {
		fmt.Fprintf(os.Stderr, "run windows service failed: %v\n", err)
		os.Exit(1)
	}
}

type windowsService struct {
	configPath string
}

func (s *windowsService) Execute(
	_ []string,
	requests <-chan svc.ChangeRequest,
	changes chan<- svc.Status,
) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	stop := make(chan struct{})
	done := make(chan error, 1)

	changes <- svc.Status{State: svc.StartPending}
	go func() {
		done <- serve(s.configPath, stop)
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepts}

	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(stop)
				if err := <-done; err != nil {
					return false, 1
				}
				return false, 0
			default:
				changes <- svc.Status{State: svc.Running, Accepts: accepts}
			}
		case err := <-done:
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}
