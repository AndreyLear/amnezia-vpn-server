package cli

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

const (
	opXrayInit   = "xray init"
	opXrayImport = "xray import"
	opXrayLink   = "xray link"
)

// cmdXray — VLESS+XHTTP+Reality рядом с AWG (эпик amnezia-vpn-server-14y0).
func (a *app) cmdXray(args []string) int {
	if len(args) == 0 {
		a.usage()
		return 2
	}
	switch args[0] {
	case "init":
		return a.cmdXrayInit(args[1:])
	case "import":
		return a.cmdXrayImport(args[1:])
	case "link":
		return a.cmdXrayLink(args[1:])
	case "entry":
		return a.cmdXrayEntry(args[1:])
	case "users-json":
		return a.cmdXrayUsersJSON(args[1:])
	default:
		a.usage()
		return 2
	}
}

// cmdXrayInit заводит ключи Reality, если их ещё нет, и применяет
// переданные флаги. Существующие ключи, shortId и путь не трогает никогда:
// они зашиты во все выданные ссылки (amnezia-vpn-server-14y0.1).
func (a *app) cmdXrayInit(args []string) int {
	parsed, err := parseArgs(args, map[string]bool{"address": true, "server-name": true, "target": true, "link-prefix": true})
	if err != nil {
		return a.usageError(opXrayInit, err.Error())
	}
	if len(parsed.positional) != 0 {
		return a.usageError(opXrayInit, "unexpected arguments")
	}
	if v, ok := parsed.flags["target"]; ok {
		if host, port, err := net.SplitHostPort(v); err != nil || host == "" || port == "" {
			return a.usageError(opXrayInit, "--target must be <host:port>")
		}
	}
	if v, ok := parsed.flags["server-name"]; ok && (v == "" || strings.ContainsAny(v, " /:")) {
		return a.usageError(opXrayInit, "--server-name must be a host name")
	}
	if v, ok := parsed.flags["address"]; ok && strings.ContainsAny(v, " /") {
		return a.usageError(opXrayInit, "--address must be a host name or an IP address")
	}

	handle, err := a.openDBForChange()
	if err != nil {
		return a.fatal(opXrayInit, err)
	}
	defer handle.Close()

	s, err := db.XraySettingsRow(handle)
	created := false
	switch {
	case errors.Is(err, db.ErrXrayNotConfigured):
		// Reality по умолчанию — self-steal на собственном домене сервера
		// (тот, что в endpoint): иначе нужен явный --server-name.
		name, ok := parsed.flags["server-name"]
		if !ok {
			if name, err = xray.DefaultServerName(handle); err != nil {
				return a.fatal(opXrayInit, err)
			}
		}
		if s, err = xray.NewServerSettings(name); err != nil {
			return a.fatal(opXrayInit, err)
		}
		created = true
	case err != nil:
		return a.fatal(opXrayInit, err)
	}
	if v, ok := parsed.flags["address"]; ok {
		s.LinkAddress = v
	}
	if v, ok := parsed.flags["server-name"]; ok {
		s.ServerName = v
	}
	if v, ok := parsed.flags["target"]; ok {
		s.Target = v
	}
	// Приставка к имени в прямых ссылках («VLESS direct andrey»). Она —
	// только подпись в приложении, но приложение различает подключения по
	// ссылке целиком: после смены люди увидят новое подключение рядом со
	// старым.
	if v, ok := parsed.flags["link-prefix"]; ok {
		s.LinkPrefix = v
	}
	if err := db.SaveXraySettings(handle, *s); err != nil {
		return a.fatal(opXrayInit, err)
	}
	if err := a.regenerate(handle); err != nil {
		return a.fatal(opXrayInit, fmt.Errorf("generate config: %w", err))
	}
	if created {
		return a.ok(opXrayInit, "reality keys generated")
	}
	return a.ok(opXrayInit, "settings updated, reality keys kept")
}

// cmdXrayImport переносит ручную настройку Xray (см. xray.Import).
func (a *app) cmdXrayImport(args []string) int {
	parsed, err := parseArgs(args, nil)
	if err != nil {
		return a.usageError(opXrayImport, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opXrayImport, "want <dir>")
	}
	handle, err := a.openDBForChange()
	if err != nil {
		return a.fatal(opXrayImport, err)
	}
	defer handle.Close()
	res, err := xray.Import(handle, parsed.positional[0])
	if err != nil {
		return a.fatal(opXrayImport, err)
	}
	if err := a.regenerate(handle); err != nil {
		return a.fatal(opXrayImport, fmt.Errorf("generate config: %w", err))
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(a.stderr, "panel %s: warning: %s\n", opXrayImport, w)
	}
	msg := fmt.Sprintf("imported reality keys and %d clients", res.Imported)
	if res.ServiceUsers > 0 {
		msg += fmt.Sprintf(", %d service users", res.ServiceUsers)
	}
	if res.Deleted > 0 {
		msg += fmt.Sprintf("; %d entries marked deleted, skipped", res.Deleted)
	}
	if len(res.Missing) > 0 {
		ids := make([]string, len(res.Missing))
		for i, id := range res.Missing {
			ids[i] = strconv.FormatInt(id, 10)
		}
		msg += "; not in the database, skipped: client_id " + strings.Join(ids, ", ")
	}
	return a.ok(opXrayImport, msg)
}

// cmdXrayLink печатает ссылку vless:// клиента.
func (a *app) cmdXrayLink(args []string) int {
	parsed, err := parseArgs(args, map[string]bool{"entry": true})
	if err != nil {
		return a.usageError(opXrayLink, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opXrayLink, "want <id>")
	}
	id, err := parseClientID(parsed.positional[0])
	if err != nil {
		return a.usageError(opXrayLink, err.Error())
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opXrayLink, err)
	}
	defer handle.Close()
	var link string
	if name, ok := parsed.flags["entry"]; ok {
		c, err := db.ClientByID(handle, id)
		if err != nil {
			return a.fatal(opXrayLink, err)
		}
		link, err = entryLink(handle, c, name)
		if err != nil {
			return a.fatal(opXrayLink, err)
		}
	} else if link, err = xray.ClientLink(handle, id); err != nil {
		return a.fatal(opXrayLink, err)
	}
	fmt.Fprintln(a.stdout, link)
	return 0
}
