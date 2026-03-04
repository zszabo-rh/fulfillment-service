/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/console"
	"github.com/osac-project/fulfillment-service/internal/database"
)

// ConsoleServerBuilder builds a ConsoleServer.
type ConsoleServerBuilder struct {
	logger    *slog.Logger
	manager   *console.Manager
	ciServer  privatev1.ComputeInstancesServer
	txManager database.TxManager
}

// consoleServer implements the Console gRPC service.
type consoleServer struct {
	publicv1.UnimplementedConsoleServer
	logger    *slog.Logger
	manager   *console.Manager
	ciServer  privatev1.ComputeInstancesServer
	txManager database.TxManager
}

// NewConsoleServer creates a new builder for the console server.
func NewConsoleServer() *ConsoleServerBuilder {
	return &ConsoleServerBuilder{}
}

func (b *ConsoleServerBuilder) SetLogger(value *slog.Logger) *ConsoleServerBuilder {
	b.logger = value
	return b
}

func (b *ConsoleServerBuilder) SetManager(value *console.Manager) *ConsoleServerBuilder {
	b.manager = value
	return b
}

func (b *ConsoleServerBuilder) SetComputeInstancesServer(value privatev1.ComputeInstancesServer) *ConsoleServerBuilder {
	b.ciServer = value
	return b
}

func (b *ConsoleServerBuilder) SetTxManager(value database.TxManager) *ConsoleServerBuilder {
	b.txManager = value
	return b
}

func (b *ConsoleServerBuilder) Build() (publicv1.ConsoleServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.manager == nil {
		return nil, errors.New("manager is mandatory")
	}
	if b.ciServer == nil {
		return nil, errors.New("compute instances server is mandatory")
	}
	if b.txManager == nil {
		return nil, errors.New("transaction manager is mandatory")
	}
	return &consoleServer{
		logger:    b.logger,
		manager:   b.manager,
		ciServer:  b.ciServer,
		txManager: b.txManager,
	}, nil
}

// Connect handles bidirectional console streaming.
func (s *consoleServer) Connect(stream publicv1.Console_ConnectServer) error {
	ctx := stream.Context()

	// Receive the init message.
	req, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to receive init message: %v", err)
	}

	init := req.GetInit()
	if init == nil {
		return status.Error(codes.InvalidArgument, "first message must be ConsoleConnectInit")
	}

	resourceType := init.GetResourceType()
	resourceID := init.GetResourceId()

	s.logger.InfoContext(ctx, "Console connect request",
		slog.String("resource_type", resourceType.String()),
		slog.String("resource_id", resourceID),
	)

	// Resolve the resource to a target.
	target, err := s.resolveTarget(ctx, resourceType, resourceID)
	if err != nil {
		return err
	}

	// Get user identity for session tracking and audit.
	subject := auth.SubjectFromContext(ctx)
	user := subject.User

	// Send connecting status.
	err = stream.Send(publicv1.ConsoleConnectResponse_builder{
		Status: publicv1.ConsoleStatus_builder{
			State:   publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTING,
			Message: fmt.Sprintf("Connecting to %s...", resourceID),
		}.Build(),
	}.Build())
	if err != nil {
		return status.Errorf(codes.Internal, "failed to send status: %v", err)
	}

	// Open the backend connection.
	conn, err := s.manager.Connect(ctx, *target, user)
	if err != nil {
		var sessionErr *console.ErrSessionExists
		if errors.As(err, &sessionErr) {
			return status.Errorf(codes.FailedPrecondition, "%v", sessionErr)
		}
		return status.Errorf(codes.Internal, "failed to connect: %v", err)
	}
	defer conn.Close()

	// Send connected status.
	err = stream.Send(publicv1.ConsoleConnectResponse_builder{
		Status: publicv1.ConsoleStatus_builder{
			State:   publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_CONNECTED,
			Message: fmt.Sprintf("Connected to %s", resourceID),
		}.Build(),
	}.Build())
	if err != nil {
		return status.Errorf(codes.Internal, "failed to send status: %v", err)
	}

	// Proxy bidirectionally.
	return s.proxy(ctx, stream, conn)
}

// proxy handles bidirectional data transfer between the gRPC stream and the backend connection.
func (s *consoleServer) proxy(ctx context.Context, stream publicv1.Console_ConnectServer, conn io.ReadWriteCloser) error {
	errCh := make(chan error, 2)

	// Backend -> client: read from backend, send to gRPC stream.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				sendErr := stream.Send(publicv1.ConsoleConnectResponse_builder{
					Output: publicv1.ConsoleOutput_builder{
						Data: append([]byte(nil), buf[:n]...),
					}.Build(),
				}.Build())
				if sendErr != nil {
					errCh <- fmt.Errorf("send to client: %w", sendErr)
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					errCh <- nil
				} else {
					errCh <- fmt.Errorf("read from backend: %w", err)
				}
				return
			}
		}
	}()

	// Client -> backend: read from gRPC stream, write to backend.
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					errCh <- nil
				} else {
					errCh <- fmt.Errorf("recv from client: %w", err)
				}
				return
			}

			if input := req.GetInput(); input != nil {
				data := input.GetData()
				if len(data) > 0 {
					_, writeErr := conn.Write(data)
					if writeErr != nil {
						errCh <- fmt.Errorf("write to backend: %w", writeErr)
						return
					}
				}
			}
			// ConsoleResize is a no-op for serial console.
		}
	}()

	// Close the backend connection when context expires (e.g., session timeout).
	// This unblocks the read goroutine which would otherwise hang forever.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	// Wait for either direction to finish.
	select {
	case err := <-errCh:
		if err != nil && ctx.Err() == nil {
			s.logger.InfoContext(ctx, "Console proxy ended with error",
				slog.Any("error", err),
			)
			return err
		}
		if ctx.Err() != nil {
			s.logger.InfoContext(ctx, "Console session timed out")
			// Send a status message before returning, best-effort.
			_ = stream.Send(publicv1.ConsoleConnectResponse_builder{
				Status: publicv1.ConsoleStatus_builder{
					State:   publicv1.ConsoleConnectionState_CONSOLE_CONNECTION_STATE_DISCONNECTED,
					Message: "Session timed out",
				}.Build(),
			}.Build())
		}
		return nil
	}
}

// resolveTarget resolves a resource type and ID to a console.Target.
func (s *consoleServer) resolveTarget(ctx context.Context, resourceType publicv1.ConsoleResourceType, resourceID string) (*console.Target, error) {
	switch resourceType {
	case publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE:
		return s.resolveComputeInstance(ctx, resourceID)
	default:
		return nil, status.Errorf(codes.Unimplemented, "unsupported resource type %q", resourceType.String())
	}
}

// resolveComputeInstance fetches a ComputeInstance from the private server and
// extracts the VM reference needed to connect.
func (s *consoleServer) resolveComputeInstance(ctx context.Context, id string) (*console.Target, error) {
	// The private server requires a database transaction in the context.
	// Streaming RPCs don't get one from the interceptor, so we create one here.
	tx, err := s.txManager.Begin(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to begin transaction: %v", err)
	}
	defer s.txManager.End(ctx, tx)

	txCtx := database.TxIntoContext(ctx, tx)
	resp, err := s.ciServer.Get(txCtx, privatev1.ComputeInstancesGetRequest_builder{
		Id: id,
	}.Build())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "compute instance %q not found: %v", id, err)
	}

	ci := resp.GetObject()
	ciStatus := ci.GetStatus()

	// Verify running state.
	if ciStatus.GetState() != privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING {
		return nil, status.Errorf(codes.FailedPrecondition,
			"compute instance %q is not running (state: %s)", id, ciStatus.GetState().String())
	}

	// Extract VM reference.
	vmRef := ciStatus.GetVmReference()
	if vmRef == nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"compute instance %q has no VM reference; it may still be provisioning", id)
	}

	return &console.Target{
		ResourceType: "compute_instance",
		ResourceID:   id,
		HubID:        vmRef.GetHubId(),
		Namespace:    vmRef.GetNamespace(),
		VMName:       vmRef.GetVmName(),
	}, nil
}

// GetAccess checks console availability for a resource.
func (s *consoleServer) GetAccess(ctx context.Context, req *publicv1.ConsoleGetAccessRequest) (*publicv1.ConsoleGetAccessResponse, error) {
	resourceType := req.GetResourceType()
	resourceID := req.GetResourceId()

	switch resourceType {
	case publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE:
		_, err := s.resolveComputeInstance(ctx, resourceID)
		if err != nil {
			st, ok := status.FromError(err)
			if ok {
				return publicv1.ConsoleGetAccessResponse_builder{
					Available: false,
					Reason:    st.Message(),
				}.Build(), nil
			}
			return nil, err
		}
		return publicv1.ConsoleGetAccessResponse_builder{
			Available:      true,
			SupportedTypes: []publicv1.ConsoleType{publicv1.ConsoleType_CONSOLE_TYPE_SERIAL},
		}.Build(), nil
	default:
		return publicv1.ConsoleGetAccessResponse_builder{
			Available: false,
			Reason:    fmt.Sprintf("unsupported resource type: %s", resourceType.String()),
		}.Build(), nil
	}
}
