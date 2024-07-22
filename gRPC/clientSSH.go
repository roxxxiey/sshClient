package gRPC

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/pin/tftp/v3"
	sh "github.com/roxxxiey/sshProto/go"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

type SSHClient struct {
	sh.UnimplementedFirmwareDeviceServer
}

func RegisterSSHClient(gRPCServer *grpc.Server) {
	sh.RegisterFirmwareDeviceServer(gRPCServer, &SSHClient{})
}

var done = make(chan bool)

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

	go func() {
		if err := tftpServer(ipTftpSever, tftpServerPort, done); err != nil {
		}
	}()

	clientConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.Password(password)},
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

	//For waiting tftp server
	time.Sleep(10 * time.Second)

	commands := []string{
		fmt.Sprintf("util tftp %s get image %s", ipTftpSever, pathToFile),
		"device upgrade image",
		"reboot",
	}

	fmt.Println("Buffer Content (as string) before FIRST COMMAND:", stdoutBuf.String())

	stdoutBuf.Reset()
	if err = s.sendCommand(stdin, commands[0]); err != nil {
		return nil, fmt.Errorf("failed to send command: %s", err)
	}

	time.Sleep(20 * time.Second)

	for !s.isBufferEmpty(stdoutBuf) {
		time.Sleep(3 * time.Second)
	}

	if !s.controlSumm(stdoutBuf) {
		return nil, fmt.Errorf("faild with file")
	}

	stdoutBuf.Reset()
	if err = s.sendCommand(stdin, commands[1]); err != nil {
		return nil, fmt.Errorf("failed to send command: %s", err)
	}
	time.Sleep(1 * time.Second)

	if !s.checkReadyToUpgrade(stdoutBuf) {
		return nil, fmt.Errorf("failed: file is not ready to upgrade")
	}

	stdoutBuf.Reset()
	if err = s.sendCommand(stdin, commands[2]); err != nil {
		return nil, fmt.Errorf("failed to send command: %s", err)
	}
	time.Sleep(1 * time.Second)

	log.Printf("Commands executed successfully")

	if err = session.Wait(); err != nil {
		return nil, fmt.Errorf("failed to wait for session: %s", err)
	}

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
	}
	// Wait for the signal to shut down
	<-done
	server.Shutdown()

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

// controlSumm is check output after util... command
func (s SSHClient) controlSumm(stdoutBuf bytes.Buffer) bool {

	stdoutData := stdoutBuf.Bytes()

	bufferString := string(stdoutData)

	scanner := bufio.NewScanner(strings.NewReader(bufferString))
	var crcValue string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "CRC16") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				crcValue = parts[2]
				break
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Println("Scanner error:", err)
		return false
	}

	if crcValue == "" {
		log.Println("CRC value is empty")
		return false
	}

	if crcValue != "0x0" {
		log.Println("CRC value is not 0x0")
		return false
	}

	return true
}

// checkReadyToUpgrade checks if the file is ready for updating
func (s SSHClient) checkReadyToUpgrade(stdoutBuf bytes.Buffer) bool {
	stdoutData := stdoutBuf.Bytes()

	bufferString := string(stdoutData)

	scanner := bufio.NewScanner(strings.NewReader(bufferString))
	var okValue string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "OK:") {
			// Разбиваем строку по двоеточию и пробелам
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				okValue = parts[0]
				break
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Println("Scanner error:", err)
		return false
	}

	if okValue == "" {
		log.Println("OK value is empty")
		return false
	}

	return true
}

func (s SSHClient) isBufferEmpty(buf bytes.Buffer) bool {
	return buf.Len() != 0
}
