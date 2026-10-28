package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/xray"
)

const (
	opXrayEntrySet    = "xray entry set"
	opXrayEntryList   = "xray entry list"
	opXrayEntryRemove = "xray entry remove"
	opXrayEntryToken  = "xray entry token"
	opXrayUsersJSON   = "xray users-json"
)

// cmdXrayEntry — узлы-входы (минимум amnezia-vpn-server-14y0.6): панель
// знает о них ровно столько, сколько нужно для ссылки «через вход».
func (a *app) cmdXrayEntry(args []string) int {
	if len(args) == 0 {
		a.usage()
		return 2
	}
	switch args[0] {
	case "set":
		return a.cmdXrayEntrySet(args[1:])
	case "list":
		return a.cmdXrayEntryList(args[1:])
	case "remove":
		return a.cmdXrayEntryRemove(args[1:])
	case "token":
		return a.cmdXrayEntryToken(args[1:])
	default:
		a.usage()
		return 2
	}
}

// cmdXrayEntrySet создаёт узел или меняет у существующего только
// переданные поля. У нового обязательно всё, кроме порта (443) и
// приставки к имени. Узел без секрета — новый или заведённый до
// автосинхронизации — получает секрет здесь же, и он показывается один раз
// (amnezia-vpn-server-14y0.6); правка полей узла с секретом его не трогает.
func (a *app) cmdXrayEntrySet(args []string) int {
	parsed, err := parseArgs(args, map[string]bool{
		"host": true, "port": true, "sni": true, "public-key": true,
		"short-id": true, "path": true, "prefix": true,
	})
	if err != nil {
		return a.usageError(opXrayEntrySet, err.Error())
	}
	if len(parsed.positional) != 1 {
		return a.usageError(opXrayEntrySet, "want <name>")
	}
	name := parsed.positional[0]
	handle, err := a.openDBForChange()
	if err != nil {
		return a.fatal(opXrayEntrySet, err)
	}
	defer handle.Close()

	e, err := db.XrayEntryByName(handle, name)
	switch {
	case errors.Is(err, db.ErrXrayEntryNotFound):
		for _, f := range []string{"host", "sni", "public-key", "short-id", "path"} {
			if _, ok := parsed.flags[f]; !ok {
				return a.usageError(opXrayEntrySet, "a new entry needs --host, --sni, --public-key, --short-id and --path")
			}
		}
		e = &db.XrayEntry{Name: name, LinkPort: 443}
	case err != nil:
		return a.fatal(opXrayEntrySet, err)
	}
	if v, ok := parsed.flags["port"]; ok {
		p, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return a.usageError(opXrayEntrySet, "--port must be a number")
		}
		e.LinkPort = p
	}
	for flag, dst := range map[string]*string{
		"host": &e.LinkHost, "sni": &e.SNI, "public-key": &e.PublicKey,
		"short-id": &e.ShortID, "path": &e.Path, "prefix": &e.LinkPrefix,
	} {
		if v, ok := parsed.flags[flag]; ok {
			*dst = v
		}
	}
	if err := db.SaveXrayEntry(handle, *e); err != nil {
		return a.fatal(opXrayEntrySet, err)
	}
	hasToken, err := entryHasToken(handle, name)
	if err != nil {
		return a.fatal(opXrayEntrySet, err)
	}
	a.ok(opXrayEntrySet, "entry "+name+" saved")
	if hasToken {
		return 0
	}
	return a.issueEntryToken(handle, opXrayEntrySet, name)
}

// cmdXrayEntryToken выдаёт узлу новый секрет взамен прежнего: прежний
// перестаёт действовать сразу, и узел надо перенастроить новым.
func (a *app) cmdXrayEntryToken(args []string) int {
	if len(args) != 1 {
		return a.usageError(opXrayEntryToken, "want <name>")
	}
	handle, err := a.openDBForChange()
	if err != nil {
		return a.fatal(opXrayEntryToken, err)
	}
	defer handle.Close()
	return a.issueEntryToken(handle, opXrayEntryToken, args[0])
}

// issueEntryToken — новый секрет узла на stdout. Это единственное место,
// где секрет виден: в базе только его хэш.
func (a *app) issueEntryToken(handle *sql.DB, op, name string) int {
	token, err := xray.RotateEntryToken(handle, name)
	if err != nil {
		return a.fatal(op, err)
	}
	return a.ok(op, "entry "+name+" token (shown only now; the node takes it as --entry-token): "+token)
}

func entryHasToken(handle db.Querier, name string) (bool, error) {
	tokens, err := db.XrayEntryTokens(handle)
	if err != nil {
		return false, err
	}
	for _, t := range tokens {
		if t.Name == name {
			return t.Hash != "", nil
		}
	}
	return false, nil
}

func (a *app) cmdXrayEntryList(args []string) int {
	if len(args) != 0 {
		return a.usageError(opXrayEntryList, "unexpected arguments")
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opXrayEntryList, err)
	}
	defer handle.Close()
	entries, err := db.XrayEntries(handle)
	if err != nil {
		return a.fatal(opXrayEntryList, err)
	}
	tokens, err := db.XrayEntryTokens(handle)
	if err != nil {
		return a.fatal(opXrayEntryList, err)
	}
	hasToken := map[string]bool{}
	for _, t := range tokens {
		hasToken[t.Name] = t.Hash != ""
	}
	// token — есть ли у узла секрет для автосинхронизации; сам секрет не
	// показывается никогда, кроме момента выдачи.
	fmt.Fprintln(a.stdout, "name\thost\tport\tsni\tpath\tshort_id\tpublic_key\tprefix\ttoken")
	for _, e := range entries {
		token := "no"
		if hasToken[e.Name] {
			token = "yes"
		}
		fmt.Fprintf(a.stdout, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%q\t%s\n",
			e.Name, e.LinkHost, e.LinkPort, e.SNI, e.Path, e.ShortID, e.PublicKey, e.LinkPrefix, token)
	}
	return 0
}

func (a *app) cmdXrayEntryRemove(args []string) int {
	if len(args) != 1 {
		return a.usageError(opXrayEntryRemove, "want <name>")
	}
	handle, err := a.openDBForChange()
	if err != nil {
		return a.fatal(opXrayEntryRemove, err)
	}
	defer handle.Close()
	if err := db.DeleteXrayEntry(handle, args[0]); err != nil {
		return a.fatal(opXrayEntryRemove, err)
	}
	return a.ok(opXrayEntryRemove, "entry "+args[0]+" removed")
}

// cmdXrayUsersJSON печатает users.json для узла-входа, настроенного
// вручную: его build.py собирает из этого списка пользователей своего Xray
// с теми же UUID. Узел с ролью entry забирает список сам
// (GET /api/entry/config, amnezia-vpn-server-14y0.6).
func (a *app) cmdXrayUsersJSON(args []string) int {
	if len(args) != 0 {
		return a.usageError(opXrayUsersJSON, "unexpected arguments")
	}
	handle, err := a.openDB()
	if err != nil {
		return a.fatal(opXrayUsersJSON, err)
	}
	defer handle.Close()
	users, err := db.VLESSUsersExport(handle)
	if err != nil {
		return a.fatal(opXrayUsersJSON, err)
	}
	enc := json.NewEncoder(a.stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(users); err != nil {
		return a.fatal(opXrayUsersJSON, err)
	}
	return 0
}

// entryLink — ссылка клиента через узел (`xray link <id> --entry <name>`).
func entryLink(handle db.Querier, c *db.ClientRecord, name string) (string, error) {
	e, err := db.XrayEntryByName(handle, name)
	if err != nil {
		return "", err
	}
	return xray.EntryLink(e, db.VLESSClient{ID: c.ID, Name: c.Name, UUID: c.VLESSUUID}), nil
}
