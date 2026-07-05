/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Identity provider lifecycle", func() {
	var (
		ctx           context.Context
		client        privatev1.IdentityProvidersClient
		tenantsClient privatev1.TenantsClient
		tenantName    string
		tenantID      string
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewIdentityProvidersClient(tool.InternalView().AdminConn())
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())

		tenantName = fmt.Sprintf("idp-test-%s", uuid.New())
		createResponse, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: tenantName,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenantID = createResponse.GetObject().GetId()

		DeferCleanup(func() {
			_, _ = tenantsClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: tenantID,
			}.Build())
		})

		waitForTenantSynced(ctx, tenantsClient, tenantID)
	})

	It("Creates an OIDC identity provider and reconciles to READY", func() {
		idpName := fmt.Sprintf("test-oidc-%s", uuid.New())
		expectedAlias := fmt.Sprintf("%s-%s", tenantName, idpName)

		createResponse, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Test OIDC Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "test-client",
						ClientSecret:     "test-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		idpID := createResponse.GetObject().GetId()

		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
				Id: idpID,
			}.Build())
		})

		Eventually(
			func(g Gomega) {
				getResponse, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
					Id: idpID,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				object := getResponse.GetObject()
				g.Expect(object.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
				g.Expect(object.GetStatus().GetPhase()).To(
					Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY),
				)
				g.Expect(object.GetStatus().GetMessage()).To(ContainSubstring(expectedAlias))
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/identity-provider/instances/%s", expectedAlias), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusOK))
	})

	It("Deletes an identity provider and removes it from Keycloak", func() {
		idpName := fmt.Sprintf("test-del-%s", uuid.New())
		expectedAlias := fmt.Sprintf("%s-%s", tenantName, idpName)

		createResponse, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Delete Test Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "test-client",
						ClientSecret:     "test-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		idpID := createResponse.GetObject().GetId()

		Eventually(
			func(g Gomega) {
				getResponse, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
					Id: idpID,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(getResponse.GetObject().GetStatus().GetPhase()).To(
					Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY),
				)
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		_, err = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
			Id: idpID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		Eventually(
			func(g Gomega) {
				_, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
					Id: idpID,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/identity-provider/instances/%s", expectedAlias), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusNotFound))
	})
})
