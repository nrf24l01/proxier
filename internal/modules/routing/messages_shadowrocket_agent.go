package routing

import "github.com/tikhonp/proxier/internal/platform/i18n"

// shadowrocketAgentMessages are the texts of handing a base config to an
// agent; the shared ones (agent.*) are the platform's. What the agent itself
// reads (the prompt, the API's answers) is English and lives in
// shadowrocket/agent.
var shadowrocketAgentMessages = i18n.Messages{
	"job.routing.shadowrocket_agent_sessions":             {EN: "Close expired agent sessions", RU: "Закрыть истёкшие сеансы агентов"},
	"job.routing.shadowrocket_agent_sessions.step.expire": {EN: "Close expired sessions", RU: "Закрыть истёкшие сеансы"},

	"event.routing.shadowrocket_agent_session_opened":       {EN: "Handed the base config of {subject} to an agent", RU: "Базовый конфиг {subject} передан агенту"},
	"event.routing.shadowrocket_agent_session_closed":       {EN: "Ended the agent session on {subject} ({reason})", RU: "Сеанс агента на {subject} завершён ({reason})"},
	"notify.routing.shadowrocket_agent_session_opened":      {EN: "An agent can change the base config of {subject}", RU: "Агент может менять базовый конфиг {subject}"},
	"notify.routing.shadowrocket_agent_session_opened.body": {EN: "Anyone holding the copied prompt can, until the session ends. The phone gets each save on its next refresh.", RU: "Любой, у кого есть скопированная подсказка, — пока сеанс не закончится. Телефон получает каждое сохранение при следующем обновлении."},

	"shadowrocket.agent.title":          {EN: "Hand off the base config to an agent", RU: "Передать базовый конфиг агенту"},
	"shadowrocket.agent.line":           {EN: "base config v{version}", RU: "базовый конфиг v{version}"},
	"shadowrocket.agent.problem.help":   {EN: "The agent gets this text as it is. Say what to keep as well as what to change, e.g. the error Shadowrocket showed.", RU: "Агент получит этот текст как есть. Напишите и что менять, и что оставить, например ошибку, которую показал Shadowrocket."},
	"shadowrocket.agent.ctx":            {EN: "The base config, what the phone gets with the routing list's rules, and a check of where the rules go are always included.", RU: "Базовый конфиг, то, что получает телефон с правилами списка, и проверка того, куда встают правила, доступны всегда."},
	"shadowrocket.agent.hidden":         {EN: "The agent reads these values as MASKED-BY-PROXIER and never sees them: {names}.", RU: "Эти значения агент видит как MASKED-BY-PROXIER и никогда не получает: {names}."},
	"shadowrocket.agent.live":           {EN: "Every save the agent makes is a new base version, and the phone gets it on its next refresh. Any version can be restored.", RU: "Каждое сохранение агента — новая версия базового конфига, и телефон получит её при следующем обновлении. Любую версию можно восстановить."},
	"shadowrocket.agent.can.1":          {EN: "read and replace this base config (each save a new version)", RU: "читать и заменять этот базовый конфиг (каждое сохранение — новая версия)"},
	"shadowrocket.agent.can.2":          {EN: "check it as a save would", RU: "проверять его так же, как при сохранении"},
	"shadowrocket.agent.can.3":          {EN: "read what the phone gets", RU: "читать то, что получает телефон"},
	"shadowrocket.agent.cant.1":         {EN: "change the routing list, the policy or the URL", RU: "менять список маршрутизации, политику или URL"},
	"shadowrocket.agent.cant.2":         {EN: "see the URL, a [Proxy] line or the MITM certificate", RU: "видеть URL, строки [Proxy] и сертификат MITM"},
	"shadowrocket.agent.cant.3":         {EN: "touch anything else", RU: "трогать что-либо ещё"},
	"shadowrocket.agent.secret.expires": {EN: "Anyone who has it can change this base config until the access expires", RU: "Любой, у кого она есть, может менять этот базовый конфиг, пока доступ не истечёт"},
	"shadowrocket.agent.secret.revoke":  {EN: "To end it sooner, revoke access on the config page.", RU: "Чтобы закончить раньше, отзовите доступ на странице конфига."},
	"shadowrocket.agent.to_config":      {EN: "Back to the config", RU: "К конфигу"},
	"shadowrocket.agent.band.counts":    {EN: "{saves} saves", RU: "сохранений: {saves}"},
	"shadowrocket.agent.band.revoked":   {EN: "The agent's access was revoked.", RU: "Доступ агента отозван."},
	"shadowrocket.agent.revoke.body":    {EN: "The token stops working at once. The versions the agent saved stay.", RU: "Токен перестанет работать сразу. Сохранённые агентом версии останутся."},
	"shadowrocket.stale.title":          {EN: "The base config changed since you opened it (an agent or another tab saved it).", RU: "Базовый конфиг изменился, пока он был открыт (его сохранил агент или другая вкладка)."},
	"shadowrocket.stale.body":           {EN: "Your text is still in the editor and nothing was overwritten: copy it out, reload, and paste what you need.", RU: "Ваш текст остался в редакторе, ничего не перезаписано: скопируйте его, перезагрузите страницу и вставьте нужное."},
	"shadowrocket.stale.reload":         {EN: "Reload", RU: "Перезагрузить"},
}

func init() {
	for k, v := range shadowrocketAgentMessages {
		messages[k] = v
	}
}
