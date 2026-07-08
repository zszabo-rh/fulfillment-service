/*
Copyright (c) 2026 Red Hat Inc.

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
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateNATGatewaysServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.NATGatewaysServer = (*PrivateNATGatewaysServer)(nil)

type PrivateNATGatewaysServer struct {
	privatev1.UnimplementedNATGatewaysServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.NATGateway]
}

func NewPrivateNATGatewaysServer() *PrivateNATGatewaysServerBuilder {
	return &PrivateNATGatewaysServerBuilder{}
}

func (b *PrivateNATGatewaysServerBuilder) SetLogger(value *slog.Logger) *PrivateNATGatewaysServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetNotifier(value events.Notifier) *PrivateNATGatewaysServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateNATGatewaysServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateNATGatewaysServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateNATGatewaysServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) Build() (result *PrivateNATGatewaysServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.NATGateway]().
		SetLogger(b.logger).
		SetService(privatev1.NATGateways_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateNATGatewaysServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateNATGatewaysServer) List(ctx context.Context,
	request *privatev1.NATGatewaysListRequest) (response *privatev1.NATGatewaysListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Get(ctx context.Context,
	request *privatev1.NATGatewaysGetRequest) (response *privatev1.NATGatewaysGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Create(ctx context.Context,
	request *privatev1.NATGatewaysCreateRequest) (response *privatev1.NATGatewaysCreateResponse, err error) {
	natGateway := request.GetObject()

	if natGateway.GetStatus() == nil {
		natGateway.SetStatus(privatev1.NATGatewayStatus_builder{
			State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING,
		}.Build())
	} else {
		natGateway.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING)
	}

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Update(ctx context.Context,
	request *privatev1.NATGatewaysUpdateRequest) (response *privatev1.NATGatewaysUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Delete(ctx context.Context,
	request *privatev1.NATGatewaysDeleteRequest) (response *privatev1.NATGatewaysDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Signal(ctx context.Context,
	request *privatev1.NATGatewaysSignalRequest) (response *privatev1.NATGatewaysSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
