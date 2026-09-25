package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCreateWebhookRejectsForbiddenDestinations(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	owner, err := st.BootstrapUser(context.Background(), "owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	ctx := WithUserID(context.Background(), owner.ID)
	project, err := st.CreateProject(ctx, "Webhook URL Validation")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	reject := []struct {
		url  string
		want string
	}{
		{"", "invalid webhook url"},
		{"not-a-url", "invalid webhook url"},
		{"ftp://example.com/hook", "url scheme must be http or https"},
		{"file:///etc/passwd", "invalid webhook url"},
		{"http://", "invalid webhook url"},
		{"http://localhost/hook", "webhook destination is not allowed"},
		{"http://LocalHost:8080/hook", "webhook destination is not allowed"},
		{"http://127.0.0.1/hook", "webhook destination is not allowed"},
		{"http://127.0.0.2/hook", "webhook destination is not allowed"},
		{"http://10.1.2.3/hook", "webhook destination is not allowed"},
		{"http://172.16.0.1/hook", "webhook destination is not allowed"},
		{"http://192.168.1.1/hook", "webhook destination is not allowed"},
		{"http://169.254.169.254/latest/meta-data/", "webhook destination is not allowed"},
		{"http://[::1]/hook", "webhook destination is not allowed"},
		{"http://[fc00::1]/hook", "webhook destination is not allowed"},
		{"http://[fe80::1]/hook", "webhook destination is not allowed"},
		{"http://[::ffff:127.0.0.1]/hook", "webhook destination is not allowed"},
	}
	for _, tc := range reject {
		_, err := st.CreateWebhook(ctx, owner.ID, CreateWebhookInput{
			ProjectID: project.ID,
			URL:       tc.url,
			Events:    []string{"*"},
		})
		if err == nil {
			t.Errorf("accepted %q", tc.url)
			continue
		}
		if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err=%v want substring %q", tc.url, err, tc.want)
		}
	}

	for _, raw := range []string{
		"https://example.com/scrumboy-hook",
		"http://example.com/scrumboy-hook",
		"https://user:pass@example.com/hook",
	} {
		wh, err := st.CreateWebhook(ctx, owner.ID, CreateWebhookInput{
			ProjectID: project.ID,
			URL:       raw,
			Events:    []string{"todo.assigned"},
		})
		if err != nil {
			t.Errorf("rejected public URL %q: %v", raw, err)
			continue
		}
		if err := st.DeleteWebhook(ctx, owner.ID, wh.ID); err != nil {
			t.Fatalf("DeleteWebhook: %v", err)
		}
	}
}
