package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"time"

	"github.com/mechta-market/pulse/internal/errs"

	"github.com/grpc-ecosystem/grpc-opentracing/go/otgrpc"
	"github.com/opentracing/opentracing-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"github.com/mechta-market/pulse/pkg/proto/common"
)

func newGrpcClientConn(uri string, secure bool, username, password, errMessagePrefix string) (*grpc.ClientConn, error) {
	if uri == "" {
		return nil, nil
	}

	errInterceptor := &grpcClientInterceptorErrorT{
		errMessagePrefix: errMessagePrefix,
	}

	dialOptions := []grpc.DialOption{
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(math.MaxInt32)),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(math.MaxInt32)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             2 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithChainUnaryInterceptor(
			otgrpc.OpenTracingClientInterceptor(opentracing.GlobalTracer()),
			errInterceptor.grpcClientInterceptorError,
		),
	}

	if secure {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(credentials.NewTLS(nil)))
	} else {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	if username != "" {
		dialOptions = append(dialOptions, grpc.WithPerRPCCredentials(
			newGrpcClientBasicAuth(username, password),
		))
	}

	conn, err := grpc.NewClient(uri, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("grpc.NewClient: %w", err)
	}

	return conn, nil
}

// error interceptor
type grpcClientInterceptorErrorT struct {
	errMessagePrefix string
}

func (o *grpcClientInterceptorErrorT) grpcClientInterceptorError(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	err := invoker(ctx, method, req, reply, cc, opts...)
	if err == nil {
		return nil
	}

	st, ok := status.FromError(err)
	if ok {
		if len(st.Details()) > 0 {
			stDetail := st.Details()[0]
			errObj, ok := stDetail.(*common.ErrorRep)
			if ok {
				return errs.ErrFull{
					Err:    errs.Err(errObj.Code),
					Desc:   o.errMessagePrefix + errObj.Message,
					Fields: errObj.Fields,
				}
			}
		}
		return errs.ErrFull{
			Err:  errs.ServiceNA,
			Desc: o.errMessagePrefix + st.String(),
		}
	}

	return fmt.Errorf(o.errMessagePrefix+": %w", err)
}

// basic auth

type grpcClientBasicAuth struct {
	metadata map[string]string
}

func newGrpcClientBasicAuth(username, password string) *grpcClientBasicAuth {
	return &grpcClientBasicAuth{
		metadata: map[string]string{
			"authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
		},
	}
}

func (o *grpcClientBasicAuth) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return o.metadata, nil
}

func (o *grpcClientBasicAuth) RequireTransportSecurity() bool {
	return false
}
