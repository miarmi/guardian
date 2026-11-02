package guardian

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

const (
	mailGraph    = "graph"
	mailSMTP     = "smtp"
	mailPractice = "practice"
)

// mailSettings is the e-mail part of Nastavení; secrets live in secrets/.
type mailSettings struct {
	Type         string
	ChildEmail   string
	ParentEmails string // comma separated
	// Microsoft 365 through Microsoft Graph (client credentials).
	Tenant, AppID, AppSecret string
	// SMTP.
	Host, Port, User, Password string
	Sender                     string
}

func (m mailSettings) parents() []string { return splitAddresses(m.ParentEmails) }

// splitAddresses reads a typed list of addresses, dropping anything without @.
func splitAddresses(s string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
		if strings.Contains(a, "@") {
			out = append(out, strings.TrimSpace(a))
		}
	}
	return out
}

// Secret mail credentials are files in secrets/, never database rows: a
// preview gets a copy of the database but not of secrets/.
const (
	mailSecretFile   = "mail-app-secret"
	mailPasswordFile = "mail-password"
)

// mailSettingKeys are the settings rows behind mailSettings.
var mailSettingKeys = []string{
	"mail_type", "mail_child", "mail_parents",
	"mail_tenant", "mail_app_id",
	"mail_host", "mail_port", "mail_user", "mail_sender",
}

func (n *notifier) mailSettings(ctx context.Context) mailSettings {
	v := n.st.Settings(ctx, mailSettingKeys...)
	m := mailSettings{
		Type: v["mail_type"], ChildEmail: v["mail_child"], ParentEmails: v["mail_parents"],
		Tenant: v["mail_tenant"], AppID: v["mail_app_id"],
		Host: v["mail_host"], Port: v["mail_port"], User: v["mail_user"], Sender: v["mail_sender"],
	}
	if m.Type == "" || (m.Type == mailPractice && !n.practice) {
		m.Type = mailGraph
	}
	m.AppSecret = readSecret(n.secretsDir, mailSecretFile)
	m.Password = readSecret(n.secretsDir, mailPasswordFile)
	return m
}

// saveMailSettings saves the form; an empty secret or password keeps the
// saved one.
func (n *notifier) saveMailSettings(ctx context.Context, m mailSettings, newSecret, newPassword string) error {
	for k, v := range map[string]string{
		"mail_type": m.Type, "mail_child": m.ChildEmail, "mail_parents": m.ParentEmails,
		"mail_tenant": m.Tenant, "mail_app_id": m.AppID,
		"mail_host": m.Host, "mail_port": m.Port, "mail_user": m.User, "mail_sender": m.Sender,
	} {
		if err := n.st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	for file, v := range map[string]string{mailSecretFile: newSecret, mailPasswordFile: newPassword} {
		if v == "" {
			continue
		}
		if err := writeSecret(n.secretsDir, file, v); err != nil {
			return err
		}
	}
	n.forgetGraphToken()
	return nil
}

// readSecret returns a secret file's trimmed content, "" when there is none.
func readSecret(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// writeSecret saves a secret readable only by Guardian.
func writeSecret(dir, name, value string) error {
	if err := ensureDir(dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600)
}
