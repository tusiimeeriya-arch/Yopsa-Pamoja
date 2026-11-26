package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yopsa/pamoja/backend/internal/auth"
	"github.com/yopsa/pamoja/backend/internal/groups"
	"github.com/yopsa/pamoja/backend/internal/rbac"
)

type fakeGroups struct {
	create func(ctx context.Context, actor rbac.Actor, in groups.CreateGroupInput, requestID string) (*groups.Group, error)
	get    func(ctx context.Context, actor rbac.Actor, groupID, requestID string) (*groups.Group, error)
}

func (f *fakeGroups) ListLegalEntities(context.Context, rbac.Actor, string) ([]groups.LegalEntity, error) {
	return nil, nil
}

func (f *fakeGroups) ListBranches(context.Context, rbac.Actor, string, string) ([]groups.Branch, error) {
	return nil, nil
}

func (f *fakeGroups) ListGroupTypes(context.Context) ([]groups.GroupType, error) {
	return []groups.GroupType{{Code: "vsla", Name: "VSLA", RulesVersion: "1"}}, nil
}

func (f *fakeGroups) CreateGroup(ctx context.Context, actor rbac.Actor, in groups.CreateGroupInput, requestID string) (*groups.Group, error) {
	return f.create(ctx, actor, in, requestID)
}

func (f *fakeGroups) GetGroup(ctx context.Context, actor rbac.Actor, groupID, requestID string) (*groups.Group, error) {
	return f.get(ctx, actor, groupID, requestID)
}

func (f *fakeGroups) ListGroups(context.Context, rbac.Actor, string, string, string) ([]groups.Group, error) {
	return nil, nil
}

func (f *fakeGroups) UpdateGroup(context.Context, rbac.Actor, string, groups.UpdateGroupInput, string) (*groups.Group, error) {
	return nil, nil
}

func (f *fakeGroups) SubmitGroup(context.Context, rbac.Actor, string, int, string) (*groups.Group, error) {
	return nil, nil
}

func (f *fakeGroups) ReviewGroup(context.Context, rbac.Actor, string, groups.ReviewInput, string) (*groups.Group, error) {
	return nil, nil
}

func (f *fakeGroups) ListMemberships(context.Context, rbac.Actor, string, string) ([]groups.Membership, error) {
	return nil, nil
}

func (f *fakeGroups) AddMembership(context.Context, rbac.Actor, string, groups.AddMembershipInput, string) (*groups.Membership, error) {
	return nil, nil
}

func (f *fakeGroups) EndMembership(context.Context, rbac.Actor, string, string, groups.EndMembershipInput, string) (*groups.Membership, error) {
	return nil, nil
}

func (f *fakeGroups) ListAuthorities(context.Context, rbac.Actor, string, string) ([]groups.Authority, error) {
	return nil, nil
}

func (f *fakeGroups) AppointAuthority(context.Context, rbac.Actor, string, groups.AppointAuthorityInput, string) (*groups.Authority, error) {
	return nil, nil
}

func (f *fakeGroups) EndAuthority(context.Context, rbac.Actor, string, string, groups.EndAuthorityInput, string) (*groups.Authority, error) {
	return nil, nil
}

func (f *fakeGroups) RecordConsent(context.Context, rbac.Actor, string, groups.RecordConsentInput, string) (*groups.Consent, error) {
	return nil, nil
}

func authenticatedDeps(t *testing.T, service GroupsService) Deps {
	t.Helper()
	userID := uuid.New()
	fake := &fakeAuth{
		authenticate: func(context.Context, string) (auth.Principal, error) {
			return auth.Principal{
				UserID: userID,
				Bindings: []rbac.Binding{{
					Role: rbac.RoleFieldOfficer, Scope: rbac.ScopeBranch, ScopeIDs: []string{"branch-1"},
				}},
			}, nil
		},
	}
	deps := testDeps(fake)
	deps.Groups = service
	return deps
}

func TestCreateGroupReturns201AndPassesActor(t *testing.T) {
	groupID := uuid.NewString()
	var seenActor rbac.Actor
	var seenInput groups.CreateGroupInput
	deps := authenticatedDeps(t, &fakeGroups{
		create: func(_ context.Context, actor rbac.Actor, in groups.CreateGroupInput, _ string) (*groups.Group, error) {
			seenActor = actor
			seenInput = in
			return &groups.Group{ID: groupID, Name: in.Name, RegistrationStatus: "draft", Version: 1}, nil
		},
	})

	rec := doJSON(t, New(deps), http.MethodPost, "/api/v1/groups",
		`{"branch_id":"11111111-1111-1111-1111-111111111111","type_code":"vsla","name":"Test Group","formed_on":"2026-01-01"}`,
		map[string]string{"Authorization": "Bearer test-token"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if seenActor.UserID == "" || len(seenActor.Bindings) != 1 {
		t.Fatalf("actor = %+v", seenActor)
	}
	if seenInput.Name != "Test Group" || seenInput.TypeCode != "vsla" {
		t.Fatalf("input = %+v", seenInput)
	}
	var body struct {
		Group groups.Group `json:"group"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Group.ID != groupID || body.Group.RegistrationStatus != "draft" {
		t.Fatalf("body = %+v", body)
	}
}

func TestGroupErrorsMapToStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"forbidden", groups.ErrForbidden, http.StatusForbidden},
		{"not found", groups.ErrNotFound, http.StatusNotFound},
		{"conflict", groups.ErrConflict, http.StatusConflict},
		{"validation", groups.ErrValidation, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := authenticatedDeps(t, &fakeGroups{
				get: func(context.Context, rbac.Actor, string, string) (*groups.Group, error) {
					return nil, tc.err
				},
			})
			rec := doJSON(t, New(deps), http.MethodGet, "/api/v1/groups/abc",
				"", map[string]string{"Authorization": "Bearer test-token"})
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestGroupRoutesRequireAuthentication(t *testing.T) {
	deps := testDeps(&fakeAuth{})
	deps.Groups = &fakeGroups{}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/groups?branch_id=11111111-1111-1111-1111-111111111111"},
		{http.MethodPost, "/api/v1/groups"},
		{http.MethodGet, "/api/v1/legal-entities"},
	} {
		rec := doJSON(t, New(deps), route.method, route.path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", route.method, route.path, rec.Code)
		}
	}
}

func TestGroupTypesArePublic(t *testing.T) {
	deps := testDeps(&fakeAuth{})
	deps.Groups = &fakeGroups{}
	rec := doJSON(t, New(deps), http.MethodGet, "/api/v1/group-types", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
