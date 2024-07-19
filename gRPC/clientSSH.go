package gRPC

import (
	"bytes"
	"context"
	"fmt"
	sh "github.com/roxxxiey/sshProto/go"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"io"
	"log"
	"os"
)

type SSHClient struct {
	sh.UnimplementedFirmwareDeviceServer
}

//var fileInHolder, err = os.CreateTemp("C:\\Users\\ПУТИН228\\GolandProjects\\SSHTFTP\\gRPC", "acceptFile")

func RegisterSSHClient(gRPCServer *grpc.Server) {
	sh.RegisterFirmwareDeviceServer(gRPCServer, &SSHClient{})
}

/*func (s *SSHClient) ChangeType(ctx context.Context, request *sh.ChangeTypeRequest) (*sh.ChangeTypeResponse, error) {
	log.Println("Call ChangeType")
	return nil, nil
}*/

func (s *SSHClient) Change(ctx context.Context, request *sh.ChangeRequest) (*sh.ChangeResponse, error) {
	log.Println("Call Change")

	settings := request.GetSettings()
	ip := settings[0].GetValue()
	user := settings[1].GetValue()
	password := settings[2].GetValue()
	pathToFile := settings[3].GetValue()
	ipTftpSever := settings[4].GetValue()
	//tftpServerPort := settings[5].GetValue()

	clientConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.Password(password)},
		//Timeout: time.Second * 10,
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
	//time.Sleep(10 * time.Second)

	commands := []string{
		fmt.Sprintf("util tftp %s get image %s", ipTftpSever, pathToFile),
		"device upgrade image",
		"reboot",
	}

	for _, cmd := range commands {
		if err = s.sendCommand(stdin, cmd); err != nil {
			return nil, fmt.Errorf("failed to send command: %s", err)
		}
		log.Printf("Command output: %s", stdoutBuf.String())
	}

	log.Printf("Commands executed successfully")

	if err = session.Wait(); err != nil {
		return nil, fmt.Errorf("failed to wait for session: %s", err)
	}
	/*
		e := os.Remove("acceptFile.txt")
		if e != nil {
			return nil, fmt.Errorf("failed to remove .txt: %s", e)
		}*/

	return &sh.ChangeResponse{
		Status: "Command executed successfully",
	}, nil
}

func (s SSHClient) sendCommand(in io.WriteCloser, command string) error {
	if _, err := in.Write([]byte(command + "\r\n")); err != nil {
		return err
	}
	return nil
}

/*func tftpServer(ipTftpSever string, tftpServerPort string, done chan bool) error {
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
}*/

// readHandler is called when client starts file download from server
/*func readHandler(filename string, rf io.ReaderFrom) error {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return err
	}
	n, err := rf.ReadFrom(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return err
	}
	fmt.Printf("%d bytes sent\n", n)
	_, err = fileInHolder.Write([]byte(fmt.Sprintf("%v", n)))
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
*/
