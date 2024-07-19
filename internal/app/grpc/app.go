package g_app

import (
	grpc_client "SSHTFTP/gRPC"
	"fmt"
	"google.golang.org/grpc"
	"log/slog"
	"net"
)

type App struct {
	log        *slog.Logger
	gRPCServer *grpc.Server
	port       int
}

// New create new gRPC server app
func New(
	log *slog.Logger,
	port int,
) *App {
	gRPCServer := grpc.NewServer()

	grpc_client.RegisterSSHClient(gRPCServer)

	return &App{
		log:        log,
		gRPCServer: gRPCServer,
		port:       port,
	}
}

func (a *App) MustRun() {
	if err := a.RunGRPCServer(); err != nil {
		panic(err)
	}

}

func (a *App) RunGRPCServer() error {
	const msg = "gRPCApp.RUN"

	log := a.log.With(
		slog.String("msg", msg),
		slog.Int("port", a.port),
	)

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", a.port))

	if err != nil {
		return fmt.Errorf("%s: %w", msg, err)
	}

	log.Info("GRPC Server is running", slog.String("addr", l.Addr().String()))

	if err := a.gRPCServer.Serve(l); err != nil {
		return fmt.Errorf("%s: %w", msg, err)
	}

	return nil
}

// Stop gRPC server
func (a *App) Stop() {
	const msg = "gRPCApp.STOP"

	a.log.With(slog.String("msg", msg)).
		Info("GRPC Server is stopping", slog.Int("port", a.port))

	a.gRPCServer.GracefulStop()
}
