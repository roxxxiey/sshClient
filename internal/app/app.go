package app

import (
	g_app "SSHTFTP/internal/app/grpc"
	"log/slog"
)

type App struct {
	GROCSrv *g_app.App
}

func New(
	log *slog.Logger,
	grpcPort int,
	storagePath string,

) *App {

	grpcApp := g_app.New(log, grpcPort)
	return &App{
		GROCSrv: grpcApp,
	}
}
