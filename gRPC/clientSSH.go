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
	"regexp"
	"strings"
	"syscall"
	"time"
)

type SSHClient struct {
	sh.UnimplementedFirmwareDeviceServer
}

func RegisterSSHClient(gRPCServer *grpc.Server) {
	sh.RegisterFirmwareDeviceServer(gRPCServer, &SSHClient{})
}

var (
	ErrWithTimeReadFile   = errors.New("i/o timeout")
	ErrWithCRC16          = errors.New("invalid CRC16 answer")
	ErrWithReadyToUpgrade = errors.New("invalid ready to upgrade")
)

func (s *SSHClient) UPDFWType(ctx context.Context, request *sh.UPDFWTypeRequest) (*sh.UPDFWTypeResponse, error) {
	log.Println("Call ChangeType")
	return nil, nil
}

// UpdateFirmware is firmware device
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
		s.def(done)
		return nil, fmt.Errorf("failed to send first command: %s", err)
	}

	if err = s.monitorConnection("CRC16", 5, 5*time.Second, &stdoutBuf); err != nil {
		s.def(done)
		return nil, fmt.Errorf("failed to monitor connection: %s", err)
	}

	time.Sleep(10 * time.Second)

	stdoutBuf.Reset()
	if err = s.sendCommand(stdin, commands[1]); err != nil {
		s.def(done)
		return nil, fmt.Errorf("failed to send second command: %s", err)
	}
	time.Sleep(1 * time.Second)

	if err = s.monitorConnection("OK", 5, 5*time.Second, &stdoutBuf); err != nil {
		s.def(done)
		return nil, fmt.Errorf("failed to monitor connection: %s", err)
	}

	stdoutBuf.Reset()
	if err = s.sendCommand(stdin, commands[2]); err != nil {
		s.def(done)
		return nil, fmt.Errorf("failed to send third command: %s", err)
	}
	time.Sleep(1 * time.Second)

	log.Printf("Commands executed successfully")

	if err = session.Wait(); err != nil && !strings.Contains(err.Error(), "wait: remote command exited without exit status or exit signal") {
		s.def(done)
		return nil, fmt.Errorf("failed to wait for session: %s", err)
	}

	s.def(done)

	return &sh.UpdateFirmwareResponse{
		Status: "Command executed successfully",
	}, nil

}

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

	n, err := rf.ReadFrom(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "READDDDDD FILE %v\n", err)
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
func (s SSHClient) monitorConnection(prefix string, attempts int, delay time.Duration, stdoutBuf *bytes.Buffer) error {
	t := time.NewTicker(delay)
	defer t.Stop()
	count := 0
	//buf := string(stdoutBuf.Bytes())
	var buf string

	for range t.C {
		buf = stdoutBuf.String()
		count++
		if !strings.Contains(buf, prefix) && count != attempts {
			log.Println("Выполняю проверку !strings.Contains(buf, prefix) && count != attempts ")
			continue
		}
		if !strings.Contains(buf, prefix) && count == attempts {
			log.Println("Выполняю проверку !strings.Contains(buf, prefix) && count == attempts ")
			return ErrWithTimeReadFile
		}

		if strings.Contains(buf, prefix) {
			log.Println("Выполняю проверку strings.Contains(buf, prefix) ")
			switch prefix {
			case "CRC16":
				log.Println("Work with CRC16")
				re := regexp.MustCompile(`CRC16 = .+`)
				matches := re.FindAllString(buf, -1)
				if len(matches) > 0 {
					// Разбиваем найденную строку по пробелам
					mass := strings.Fields(matches[0])
					if len(mass) >= 3 {
						value := strings.TrimSpace(mass[2])
						if value != "0x0" {
							log.Println("CRC value is not 0x0")
							return ErrWithCRC16
						}
					} else {
						log.Println("Unexpected format of CRC16 line")
						return ErrWithCRC16
					}
				} else {
					log.Println("CRC16 line not found")
					return ErrWithCRC16
				}
			case "OK":
				re := regexp.MustCompile(`OK:`)
				matches := re.FindAllString(buf, -1)
				if len(matches) > 0 {

				} else {
					return ErrWithReadyToUpgrade
				}
			}
			break
		}
	}
	return nil
}

func (s SSHClient) def(done chan bool) {
	select {
	case done <- true:
		log.Println("Canal is open")
	default:
		log.Println("Channel is close")
	}
}
