// Журнал панели (amnezia-vpn-server-gqep).
//
// Панель меняет то, что видит пользователь: клиентов включают, отключают,
// переименовывают, меняют им MTU. Ничего из этого не записывалось — ни кто,
// ни когда. При одном администраторе это прежде всего защита от «я такого не
// делал»: с записью видно, было действие или не было. При появлении второго
// администратора это станет необходимостью.
//
// СЕКРЕТАМ ЗДЕСЬ НЕ МЕСТО. Ни ключей, ни паролей, ни даже неверно введённого
// пароля при неудачном входе: журнал должен пережить кражу базы, не добавив
// вору ничего сверх того, что он в ней и так нашёл.
package web

import (
	"net/http"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

// Действия журнала. Строки короткие и машинные: расшифровывает их панель,
// и переводить их здесь значило бы хранить перевод в базе.
const (
	auditLogin        = "login"
	auditLoginFailed  = "login.failed"
	auditLogout       = "logout"
	auditClientAdd    = "client.add"
	auditClientEdit   = "client.edit"
	auditClientMTU    = "client.mtu"
	auditClientToggle = "client.toggle"
	auditClientDelete = "client.delete"
	auditRestore      = "backup.restore"
)

// actorOf — кто это сделал. Для неудачного входа сессии ещё нет, и имя
// приходит из самой попытки.
func actorOf(r *http.Request) string {
	if sess, ok := auth.CurrentUser(r.Context()); ok {
		return sess.Username
	}
	return ""
}

// audit записывает строку журнала и НЕ мешает работе, если не смогла.
//
// Журнал существует ради разбирательств, а не вместо работы: клиент,
// которого не удалось записать, всё равно должен быть создан. Иначе отказ
// журнала превратился бы в отказ панели — то есть в аварию из-за средства
// расследования аварий.
func (s *Server) audit(r *http.Request, action, subject, detail string) {
	s.auditAs(actorOf(r), action, subject, detail)
}

func (s *Server) auditAs(actor, action, subject, detail string) {
	if err := db.AuditAppend(s.db(), actor, action, subject, detail); err != nil {
		s.cfg.Logger.Printf("audit %s: %v", action, err)
	}
}
