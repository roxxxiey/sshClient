package gRPC

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/pin/tftp/v3"
	sh "github.com/roxxxiey/sshProto/go"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type SSHClient struct {
	sh.UnimplementedFirmwareDeviceServer
}

const (
	Type = "SSH"
)

var stderrBuf bytes.Buffer

var fileTransferWG sync.WaitGroup

func RegisterSSHClient(gRPCServer *grpc.Server) {
	sh.RegisterFirmwareDeviceServer(gRPCServer, &SSHClient{})
}

var (
	// ErrWithTimeReadFile this error works when there are problems with downloading a file
	ErrWithTimeReadFile   = errors.New("problems with downloading a file: i/o timeout")
	ErrWithCRC16          = errors.New("invalid CRC16 answer (is not equal 0x0)")
	ErrWithReadyToUpgrade = errors.New("device is not ready to upgrade")
)

// UPDFWType has not been implemented yet
func (s *SSHClient) UPDFWType(ctx context.Context, request *sh.UPDFWTypeRequest) (*sh.UPDFWTypeResponse, error) {
	log.Println("Call ChangeType")
	return nil, nil
}

// UpdateFirmware  firmwares device
func (s *SSHClient) UpdateFirmware(ctx context.Context, request *sh.UpdateFirmwareRequest) (*sh.UpdateFirmwareResponse, error) {
	log.Println("Call UpdateFirmware")

	settings := request.GetSettings()
	ip := settings[0].GetValue()
	user := settings[1].GetValue()
	password := settings[2].GetValue()
	pathToFile := settings[3].GetValue()
	ipTftpSever := settings[4].GetValue()
	tftpServerPort := settings[5].GetValue()

	clientConfig := &ssh.ClientConfig{
		User:    user,
		Auth:    []ssh.AuthMethod{ssh.Password(password)},
		Timeout: 1 * time.Minute,
	}
	clientConfig.HostKeyCallback = ssh.InsecureIgnoreHostKey()

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", ip), clientConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH Dial: %s", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("SSH NewSession: %s", err)
	}
	defer session.Close()

	log.Println("Session created")

	stdin, err := session.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("unable to setup STDIN", err.Error())
	}
	go io.Copy(stdin, os.Stdin)

	defer stdin.Close()

	stderr, err := session.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("Unable to setup stderr for session: %v", err)
	}
	go io.Copy(os.Stderr, stderr)

	var stdoutBuf bytes.Buffer
	session.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)

	if err = session.Shell(); err != nil {
		fmt.Errorf("unable to setup STDOUT", err.Error())
	}

	////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
	done := make(chan bool)

	go func() {
		if err := tftpServer(ipTftpSever, tftpServerPort, done); err != nil {
			done <- true
		}
	}()
	////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

	//For waiting tftp server
	time.Sleep(5 * time.Second)

	commands := []string{
		fmt.Sprintf("util tftp %s get image %s", ipTftpSever, pathToFile),
		"device upgrade image",
		"reboot",
	}

	fmt.Println("Buffer Content (as string) before first COMMAND:", stdoutBuf.String())

	stdoutBuf.Reset()
	if err = s.sendCommand(stdin, commands[0]); err != nil {
		s.checkChanalStatus(done)
		return nil, fmt.Errorf("failed to send first command: %s", err)
	}

	if err = s.monitorConnection("CRC16 = 0x0", 5, 5*time.Second, &stdoutBuf, &stderrBuf); err != nil {
		s.checkChanalStatus(done)
		return nil, fmt.Errorf("failed to monitor connection: %s", err)
	}

	time.Sleep(10 * time.Second)

	stdoutBuf.Reset()
	if err = s.sendCommand(stdin, commands[1]); err != nil {
		s.checkChanalStatus(done)
		return nil, fmt.Errorf("failed to send second command: %s", err)
	}
	time.Sleep(1 * time.Second)

	if err = s.monitorConnection("OK: device is ready for upgrade", 5, 5*time.Second, &stdoutBuf, &stderrBuf); err != nil {
		s.checkChanalStatus(done)
		return nil, fmt.Errorf("failed to monitor connection: %s", err)
	}

	stdoutBuf.Reset()
	if err = s.sendCommand(stdin, commands[2]); err != nil {
		s.checkChanalStatus(done)
		return nil, fmt.Errorf("failed to send third command: %s", err)
	}
	time.Sleep(1 * time.Second)

	log.Printf("Commands executed successfully")

	if err = session.Wait(); err != nil && !strings.Contains(err.Error(), "wait: remote command exited without exit status or exit signal") {
		s.checkChanalStatus(done)
		return nil, fmt.Errorf("failed to wait for session: %s", err)
	}

	s.checkChanalStatus(done)

	return &sh.UpdateFirmwareResponse{
		Status: "Command executed successfully",
	}, nil

}

// Preset has not been implemented yet
func (s *SSHClient) Preset(ctx context.Context, request *sh.PresetRequest) (*sh.PresetResponse, error) {
	log.Println("Call Preset")
	return nil, nil
}

// sendCommand is send commands on work
func (s SSHClient) sendCommand(in io.WriteCloser, command string) error {
	if _, err := in.Write([]byte(command + "\r\n")); err != nil {
		return err
	}
	return nil
}

func tftpServer(ipTftpSever string, tftpServerPort string, done chan bool) error {
	server := tftp.NewServer(readHandler, writeHandler)
	server.SetTimeout(5 * time.Second)
	addr := fmt.Sprintf("%s:%s", ipTftpSever, tftpServerPort)
	err := server.ListenAndServe(addr)
	if err != nil {
		return fmt.Errorf("tftp server ListenAndServe: %s", err)
		done <- true
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-signalChan:
		log.Println("Received termination signal. Shutting down...")
	case <-done:
		log.Println("Received shutdown signal from done channel.")
	}

	server.Shutdown()
	log.Println("server shut down")
	return nil
}

// readHandler is called when client starts file download from server
func readHandler(filename string, rf io.ReaderFrom) error {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return err
	}
	defer file.Close()

	fileTransferWG.Add(1)
	defer fileTransferWG.Done()

	n, err := rf.ReadFrom(file)
	if err != nil {
		fmt.Fprintf(&stderrBuf, "READDDDDD FILE %v\n", err)
		return err
	}
	fmt.Printf("%d bytes sent\n", n)
	if err != nil {
		return err
	}
	return nil
}

// writeHandler is called when client starts file upload to server
func writeHandler(filename string, wt io.WriterTo) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return err
	}
	n, err := wt.WriteTo(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return err
	}
	fmt.Printf("%d bytes received\n", n)
	return nil
}

// monitorConnection monitors the status of the connection and notifies when it is interrupted
func (s SSHClient) monitorConnection(prefix string, attempts int, delay time.Duration, stdoutBuf *bytes.Buffer, stderrBuf *bytes.Buffer) error {
	t := time.NewTicker(delay)
	defer t.Stop()
	count := 0
	var bufOut string
	var bufErr string

	for range t.C {
		bufOut = stdoutBuf.String()
		count++
		if !strings.Contains(bufOut, prefix) && count != attempts {
			log.Println("Выполняю проверку !strings.Contains(bufOut, prefix) && count != attempts ")
			continue
		}
		if !strings.Contains(bufOut, prefix) && count == attempts {
			log.Println("Выполняю проверку !strings.Contains(bufOut, prefix) && count == attempts ")
			fileTransferWG.Wait()
			log.Println(stderrBuf.String())
			bufErr = stderrBuf.String()
			log.Println(bufErr)
			if strings.Contains(bufErr, "READDDDDD FILE read udp [::]:") {
				log.Printf("Ошибка из stderr: %s", bufErr)
				stderrBuf.Reset()
				return ErrWithTimeReadFile
			}
			return ErrWithCRC16
		}

		if strings.Contains(bufOut, prefix) {
			log.Println("Выполняю проверку strings.Contains(bufOut, prefix) ")
			break
		}
	}
	return nil
}

// checkChanalStatus check channal status
func (s SSHClient) checkChanalStatus(done chan bool) {
	select {
	case done <- true:
		log.Println("Canal is open")
	default:
		log.Println("Channel is close")
	}
}
