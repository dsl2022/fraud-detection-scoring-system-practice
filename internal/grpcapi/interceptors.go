package grpcapi

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/blocklocmedia/fraud-signals/internal/auth"
	"github.com/blocklocmedia/fraud-signals/internal/reqid"
)

// ChainUnary builds the unary interceptor stack. Order mirrors the HTTP edge:
// request-id (outermost) -> logging -> auth -> handler. We reuse the SAME
// reqid/auth packages as HTTP so a request is treated identically over either
// transport.
func ChainUnary(v auth.Validator, log *slog.Logger) grpc.ServerOption {
	return grpc.ChainUnaryInterceptor(
		requestIDInterceptor(),
		loggingInterceptor(log),
		authInterceptor(v, log),
	)
}

func requestIDInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		id := metadataValue(ctx, reqid.HeaderName)
		if id == "" {
			id = reqid.Generate()
		}
		return handler(reqid.NewContext(ctx, id), req)
	}
}

func loggingInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err)
		log.LogAttrs(ctx, slog.LevelInfo, "grpc_request",
			slog.String("request_id", reqid.FromContext(ctx)),
			slog.String("method", info.FullMethod),
			slog.String("code", code.String()),
			slog.Duration("duration", time.Since(start)),
		)
		return resp, err
	}
}

// authInterceptor validates the bearer token from the "authorization" metadata.
// A nil validator disables auth (dev), matching the HTTP edge's behaviour.
func authInterceptor(v auth.Validator, log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if v == nil {
			return handler(ctx, req)
		}
		token := auth.BearerToken(metadataValue(ctx, "authorization"))
		claims, err := v.Validate(ctx, token)
		if err != nil {
			log.WarnContext(ctx, "grpc auth rejected",
				"request_id", reqid.FromContext(ctx), "error", err.Error())
			return nil, status.Error(codes.Unauthenticated, "invalid or missing credentials")
		}
		return handler(auth.NewContext(ctx, claims), req)
	}
}

// metadataValue reads a single (case-insensitive) metadata key, or "".
func metadataValue(ctx context.Context, key string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if vals := md.Get(key); len(vals) > 0 {
		return vals[0]
	}
	return ""
}
