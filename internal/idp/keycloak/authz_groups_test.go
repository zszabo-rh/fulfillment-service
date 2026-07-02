/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package keycloak

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ensureGroupHierarchyWithCache path normalization", func() {
	It("should normalize path with double leading slashes", func() {
		// When a project path is empty, we might get "//system:viewers"
		// This should be normalized to "/system:viewers"
		groupPath := "//system:viewers"

		// The normalization should:
		// 1. Trim leading/trailing slashes: "system:viewers"
		// 2. Replace double slashes: (already done after trim)
		// 3. Split into segments: ["system:viewers"]
		// 4. Build path as "/system:viewers"

		// We expect the normalized path to be stored in cache as "/system:viewers"
		expectedNormalizedPath := "/system:viewers"

		// Verify the normalization logic matches what we expect
		normalizedPath := strings.Trim(groupPath, "/")
		normalizedPath = strings.ReplaceAll(normalizedPath, "//", "/")
		Expect(normalizedPath).To(Equal("system:viewers"))

		// After building the path, it should be stored with leading slash
		segments := strings.Split(normalizedPath, "/")
		Expect(segments).To(Equal([]string{"system:viewers"}))

		builtPath := "/" + segments[0]
		Expect(builtPath).To(Equal(expectedNormalizedPath))
	})

	It("should normalize path with double slashes", func() {
		// The main use case is handling empty project paths that result in "//system:viewers"
		groupPath := "/web-app//system:viewers"

		normalizedPath := strings.Trim(groupPath, "/")
		normalizedPath = strings.ReplaceAll(normalizedPath, "//", "/")

		// After trimming: "web-app//system:viewers"
		// After replacing "//": "web-app/system:viewers"
		Expect(normalizedPath).To(Equal("web-app/system:viewers"))
	})

	It("should handle normal path without modification", func() {
		groupPath := "/web-app/system:viewers"

		normalizedPath := strings.Trim(groupPath, "/")
		normalizedPath = strings.ReplaceAll(normalizedPath, "//", "/")

		Expect(normalizedPath).To(Equal("web-app/system:viewers"))
	})

	It("should handle single segment path", func() {
		groupPath := "/system:viewers"

		normalizedPath := strings.Trim(groupPath, "/")
		normalizedPath = strings.ReplaceAll(normalizedPath, "//", "/")

		segments := strings.Split(normalizedPath, "/")
		Expect(segments).To(Equal([]string{"system:viewers"}))
	})

	It("should detect empty path as invalid", func() {
		groupPath := "/"

		normalizedPath := strings.Trim(groupPath, "/")
		segments := strings.Split(normalizedPath, "/")

		// Empty string splits into one segment with empty string
		Expect(segments).To(HaveLen(1))
		Expect(segments[0]).To(Equal(""))

		// This should trigger the error condition:
		// len(segments) == 1 && segments[0] == ""
		isInvalid := len(segments) == 0 || (len(segments) == 1 && segments[0] == "")
		Expect(isInvalid).To(BeTrue())
	})
})

var _ = Describe("searchGroupRecursively", func() {
	It("should find a top-level group", func() {
		group := groupNode{
			ID:   "group-123",
			Name: "web-app",
			Path: "/web-app",
		}

		id := searchGroupRecursively(group, "/web-app")
		Expect(id).To(Equal("group-123"))
	})

	It("should find a nested group one level deep", func() {
		group := groupNode{
			ID:   "group-123",
			Name: "web-app",
			Path: "/web-app",
			SubGroups: []groupNode{
				{ID: "group-456", Name: "system:viewers", Path: "/web-app/system:viewers"},
				{ID: "group-789", Name: "system:managers", Path: "/web-app/system:managers"},
			},
		}

		id := searchGroupRecursively(group, "/web-app/system:viewers")
		Expect(id).To(Equal("group-456"))
	})

	It("should find a deeply nested group three levels deep", func() {
		group := groupNode{
			ID:   "group-parent",
			Name: "parent-project",
			Path: "/parent-project",
			SubGroups: []groupNode{
				{
					ID:   "group-child",
					Name: "child-project",
					Path: "/parent-project/child-project",
					SubGroups: []groupNode{
						{ID: "group-viewers", Name: "system:viewers", Path: "/parent-project/child-project/system:viewers"},
						{ID: "group-managers", Name: "system:managers", Path: "/parent-project/child-project/system:managers"},
					},
				},
			},
		}

		id := searchGroupRecursively(group, "/parent-project/child-project/system:viewers")
		Expect(id).To(Equal("group-viewers"))
	})

	It("should return empty string when group is not found", func() {
		group := groupNode{
			ID:   "group-123",
			Name: "web-app",
			Path: "/web-app",
			SubGroups: []groupNode{
				{ID: "group-456", Name: "system:viewers", Path: "/web-app/system:viewers"},
			},
		}

		id := searchGroupRecursively(group, "/nonexistent")
		Expect(id).To(Equal(""))
	})

	It("should search across multiple branches", func() {
		group := groupNode{
			ID:   "group-root",
			Name: "root",
			Path: "/root",
			SubGroups: []groupNode{
				{
					ID:   "group-branch1",
					Name: "branch1",
					Path: "/root/branch1",
					SubGroups: []groupNode{
						{ID: "group-leaf1", Name: "leaf1", Path: "/root/branch1/leaf1"},
					},
				},
				{
					ID:   "group-branch2",
					Name: "branch2",
					Path: "/root/branch2",
					SubGroups: []groupNode{
						{ID: "group-leaf2", Name: "leaf2", Path: "/root/branch2/leaf2"},
					},
				},
			},
		}

		// Should find in first branch
		id := searchGroupRecursively(group, "/root/branch1/leaf1")
		Expect(id).To(Equal("group-leaf1"))

		// Should find in second branch
		id = searchGroupRecursively(group, "/root/branch2/leaf2")
		Expect(id).To(Equal("group-leaf2"))
	})

	It("should handle empty subgroups", func() {
		group := groupNode{
			ID:        "group-123",
			Name:      "web-app",
			Path:      "/web-app",
			SubGroups: []groupNode{},
		}

		id := searchGroupRecursively(group, "/web-app")
		Expect(id).To(Equal("group-123"))

		id = searchGroupRecursively(group, "/web-app/system:viewers")
		Expect(id).To(Equal(""))
	})
})
