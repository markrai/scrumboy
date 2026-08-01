package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestBoardSlugReadAccess_RESTContract(t *testing.T) {
	t.Run("full mode", func(t *testing.T) {
		ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()

		st := store.New(sqlDB, nil)
		signedOut := newCookieClient(t)

		preBootstrapProject, err := st.CreateProject(context.Background(), "Pre-bootstrap Slug Access")
		if err != nil {
			t.Fatalf("CreateProject before bootstrap: %v", err)
		}
		const preBootstrapTodo = "pre-bootstrap slug access todo"
		seedSlugBoardTodo(
			t,
			st,
			context.Background(),
			preBootstrapProject.ID,
			preBootstrapTodo,
			store.ModeFull,
		)
		for _, route := range slugBoardReadRoutes {
			route := route
			t.Run(route.name+"/pre-bootstrap durable project", func(t *testing.T) {
				assertSlugBoardReadSuccess(
					t,
					signedOut,
					ts.URL,
					route,
					preBootstrapProject,
					preBootstrapTodo,
				)
			})
		}

		ownerClient := newCookieClient(t)
		ownerJSON := bootstrapUserClient(
			t,
			ownerClient,
			ts.URL,
			"Owner",
			"board-slug-access-owner@example.com",
			"password123",
		)
		ownerID := int64(ownerJSON["id"].(float64))
		ctxOwner := store.WithUserID(context.Background(), ownerID)

		durableProject, err := st.CreateProject(ctxOwner, "Durable Slug Access")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		const durableTodo = "durable slug access todo"
		seedSlugBoardTodo(t, st, ctxOwner, durableProject.ID, durableTodo, store.ModeFull)

		viewer, err := st.CreateUser(
			context.Background(),
			"board-slug-access-viewer@example.com",
			"password123",
			"Viewer",
		)
		if err != nil {
			t.Fatalf("CreateUser(viewer): %v", err)
		}
		if err := st.AddProjectMember(
			ctxOwner,
			ownerID,
			durableProject.ID,
			viewer.ID,
			store.RoleViewer,
		); err != nil {
			t.Fatalf("AddProjectMember(viewer): %v", err)
		}
		viewerClient := newCookieClient(t)
		loginUserClient(
			t,
			viewerClient,
			ts.URL,
			"board-slug-access-viewer@example.com",
			"password123",
		)

		_, err = st.CreateUser(
			context.Background(),
			"board-slug-access-outsider@example.com",
			"password123",
			"Outsider",
		)
		if err != nil {
			t.Fatalf("CreateUser(outsider): %v", err)
		}
		outsiderClient := newCookieClient(t)
		loginUserClient(
			t,
			outsiderClient,
			ts.URL,
			"board-slug-access-outsider@example.com",
			"password123",
		)

		activeExpiringProject, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("CreateAnonymousBoard(active): %v", err)
		}
		const activeExpiringTodo = "active expiring slug access todo"
		seedSlugBoardTodo(
			t,
			st,
			context.Background(),
			activeExpiringProject.ID,
			activeExpiringTodo,
			store.ModeFull,
		)
		expiredProject, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("CreateAnonymousBoard(expired): %v", err)
		}
		if _, err := sqlDB.Exec(
			`UPDATE projects SET expires_at = ? WHERE id = ?`,
			time.Now().UTC().Add(-time.Hour).UnixMilli(),
			expiredProject.ID,
		); err != nil {
			t.Fatalf("expire project: %v", err)
		}

		for _, route := range slugBoardReadRoutes {
			route := route
			t.Run(route.name, func(t *testing.T) {
				t.Run("authenticated owner", func(t *testing.T) {
					assertSlugBoardReadSuccess(
						t,
						ownerClient,
						ts.URL,
						route,
						durableProject,
						durableTodo,
					)
				})
				t.Run("authenticated viewer", func(t *testing.T) {
					assertSlugBoardReadSuccess(
						t,
						viewerClient,
						ts.URL,
						route,
						durableProject,
						durableTodo,
					)
				})
				t.Run("signed-out durable project", func(t *testing.T) {
					assertSlugBoardReadNotFound(
						t,
						signedOut,
						ts.URL,
						route,
						durableProject.Slug,
						"",
					)
				})
				t.Run("authenticated non-member", func(t *testing.T) {
					assertSlugBoardReadNotFound(
						t,
						outsiderClient,
						ts.URL,
						route,
						durableProject.Slug,
						"",
					)
				})
				t.Run("active expiring project is link-readable", func(t *testing.T) {
					assertSlugBoardReadSuccess(
						t,
						signedOut,
						ts.URL,
						route,
						activeExpiringProject,
						activeExpiringTodo,
					)
				})
				t.Run("expired expiring project", func(t *testing.T) {
					assertSlugBoardReadNotFound(
						t,
						signedOut,
						ts.URL,
						route,
						expiredProject.Slug,
						"",
					)
				})
				t.Run("nonexistent project", func(t *testing.T) {
					assertSlugBoardReadNotFound(
						t,
						signedOut,
						ts.URL,
						route,
						"missing-slug-access",
						"",
					)
				})
			})
		}
	})

	t.Run("anonymous mode", func(t *testing.T) {
		ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
		defer cleanup()

		st := store.New(sqlDB, nil)
		client := ts.Client()

		activeProject, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("CreateAnonymousBoard(active): %v", err)
		}
		const activeTodo = "anonymous mode slug access todo"
		seedSlugBoardTodo(
			t,
			st,
			context.Background(),
			activeProject.ID,
			activeTodo,
			store.ModeAnonymous,
		)
		expiredProject, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("CreateAnonymousBoard(expired): %v", err)
		}
		if _, err := sqlDB.Exec(
			`UPDATE projects SET expires_at = ? WHERE id = ?`,
			time.Now().UTC().Add(-time.Hour).UnixMilli(),
			expiredProject.ID,
		); err != nil {
			t.Fatalf("expire anonymous project: %v", err)
		}
		durableProject, err := st.CreateProject(context.Background(), "Existing Anonymous-mode Durable")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		const durableTodo = "anonymous mode durable slug access todo"
		seedSlugBoardTodo(
			t,
			st,
			context.Background(),
			durableProject.ID,
			durableTodo,
			store.ModeAnonymous,
		)

		for _, route := range slugBoardReadRoutes {
			route := route
			t.Run(route.name, func(t *testing.T) {
				t.Run("active anonymous board", func(t *testing.T) {
					assertSlugBoardReadSuccess(t, client, ts.URL, route, activeProject, activeTodo)
				})
				t.Run("expired anonymous board", func(t *testing.T) {
					assertSlugBoardReadNotFound(
						t,
						client,
						ts.URL,
						route,
						expiredProject.Slug,
						"",
					)
				})
				t.Run("ownerless durable project created directly in auth-disabled database", func(t *testing.T) {
					assertSlugBoardReadSuccess(
						t,
						client,
						ts.URL,
						route,
						durableProject,
						durableTodo,
					)
				})
				t.Run("nonexistent board", func(t *testing.T) {
					assertSlugBoardReadNotFound(
						t,
						client,
						ts.URL,
						route,
						"missing-anonymous-slug",
						"",
					)
				})
			})
		}
	})

	t.Run("normally owned durable project after full-to-anonymous transition", func(t *testing.T) {
		fullServer, sqlDB, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()

		ownerClient := newCookieClient(t)
		ownerJSON := bootstrapUserClient(
			t,
			ownerClient,
			fullServer.URL,
			"Owner",
			"board-slug-access-mode-transition-owner@example.com",
			"password123",
		)
		ownerID := int64(ownerJSON["id"].(float64))
		ctxOwner := store.WithUserID(context.Background(), ownerID)
		st := store.New(sqlDB, nil)

		durableProject, err := st.CreateProject(ctxOwner, "Full-to-Anonymous Durable")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		seedSlugBoardTodo(
			t,
			st,
			ctxOwner,
			durableProject.ID,
			"full-to-anonymous durable todo",
			store.ModeFull,
		)

		anonymousServer := httptest.NewServer(NewServer(st, Options{
			MaxRequestBody: 1 << 20,
			ScrumboyMode:   "anonymous",
		}))
		defer anonymousServer.Close()

		for _, route := range slugBoardReadRoutes {
			route := route
			t.Run(route.name, func(t *testing.T) {
				assertSlugBoardReadNotFound(
					t,
					anonymousServer.Client(),
					anonymousServer.URL,
					route,
					durableProject.Slug,
					"",
				)
			})
		}
	})
}

func TestBoardSlugReadAccess_RESTPrecedesValidation(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	ownerJSON := bootstrapUserClient(
		t,
		ownerClient,
		ts.URL,
		"Owner",
		"board-slug-access-order-owner@example.com",
		"password123",
	)
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	durableProject, err := st.CreateProject(ctxOwner, "Slug Access Order")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	_, err = st.CreateUser(
		context.Background(),
		"board-slug-access-order-outsider@example.com",
		"password123",
		"Outsider",
	)
	if err != nil {
		t.Fatalf("CreateUser(outsider): %v", err)
	}
	outsiderClient := newCookieClient(t)
	loginUserClient(
		t,
		outsiderClient,
		ts.URL,
		"board-slug-access-order-outsider@example.com",
		"password123",
	)
	signedOut := newCookieClient(t)

	activeExpiringProject, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard(active): %v", err)
	}
	expiredProject, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard(expired): %v", err)
	}
	if _, err := sqlDB.Exec(
		`UPDATE projects SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Hour).UnixMilli(),
		expiredProject.ID,
	); err != nil {
		t.Fatalf("expire project: %v", err)
	}

	for _, route := range slugBoardReadRoutes {
		route := route
		t.Run(route.name, func(t *testing.T) {
			t.Run("authorized durable project reaches validation", func(t *testing.T) {
				assertSlugBoardInvalidAssignee(
					t,
					ownerClient,
					ts.URL,
					route,
					durableProject.Slug,
				)
			})
			t.Run("active expiring project reaches validation", func(t *testing.T) {
				assertSlugBoardInvalidAssignee(
					t,
					signedOut,
					ts.URL,
					route,
					activeExpiringProject.Slug,
				)
			})
			t.Run("signed-out durable project hides before validation", func(t *testing.T) {
				assertSlugBoardReadNotFound(
					t,
					signedOut,
					ts.URL,
					route,
					durableProject.Slug,
					"?assignee=abc",
				)
			})
			t.Run("authenticated non-member hides before validation", func(t *testing.T) {
				assertSlugBoardReadNotFound(
					t,
					outsiderClient,
					ts.URL,
					route,
					durableProject.Slug,
					"?assignee=abc",
				)
			})
			t.Run("expired project hides before validation", func(t *testing.T) {
				assertSlugBoardReadNotFound(
					t,
					signedOut,
					ts.URL,
					route,
					expiredProject.Slug,
					"?assignee=abc",
				)
			})
			t.Run("nonexistent project hides before validation", func(t *testing.T) {
				assertSlugBoardReadNotFound(
					t,
					signedOut,
					ts.URL,
					route,
					"missing-slug-access-order",
					"?assignee=abc",
				)
			})
		})
	}
}
