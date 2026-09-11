// История скорости клиента (amnezia-vpn-server-8lnv).
//
// Данные собирает контейнер awg рядом со status.json; панель только
// читает их и сворачивает под ширину графика. Свёртка живёт здесь, а не
// в браузере, потому что сутки в подробном виде — сотни килобайт на
// каждое открытие карточки, а свёрнутые — десяток.
package web

import (
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/status"
)

// Окна: десять минут — «тормозит прямо сейчас», сутки — «найди вчерашний
// вечер». Третьего не нужно: недели у нас нет по данным.
//
// Час отсюда убран (amnezia-vpn-server-teos). Он вмещал 720 замеров, и на
// любой разумной ширине графика они сворачивались по несколько в столбец —
// та самая мелочь, ради которой график и открывают, усреднялась и
// пропадала. Десять минут при такте записи в пять секунд — это ровно 120
// замеров, то есть один замер на столбец и никакой свёртки.
const (
	speedWindow10Min = "10min"
	speedWindowDay   = "day"
)

// speed10MinSpan — почему именно десять минут: см. комментарий к окнам.
const speed10MinSpan = 10 * time.Minute

// speedMaxColumns — потолок на число столбцов. Он не про красоту, а про
// то, что запрос считает на сервере: ширины экрана хватает с запасом, а
// без потолка любой мог бы попросить миллион.
const speedMaxColumns = 2000

// speedDefaultColumns — если ширину не сказали.
const speedDefaultColumns = 700

// speedJSON отдаёт столбцы параллельными рядами чисел, а не списком
// объектов: на 700 столбцах это разница между десятью килобайтами и
// восемьюдесятью, а читать их всё равно по индексу.
//
// null означает разрыв — «замеров не было». Это НЕ ноль: ноль значит
// «клиент ничего не получал» и является диагнозом, а разрыв значит «мы не
// смотрели» и диагнозом не является.
type speedJSON struct {
	Window  string `json:"window"`
	FromUTC string `json:"from_utc"`
	ToUTC   string `json:"to_utc"`
	// Down — к клиенту, Up — от него. Названия от лица клиента, потому
	// что график живёт в его карточке.
	DownMin []*uint64 `json:"down_min_bps"`
	DownMax []*uint64 `json:"down_max_bps"`
	UpMin   []*uint64 `json:"up_min_bps"`
	UpMax   []*uint64 `json:"up_max_bps"`
	// Online — был ли клиент на связи в этом столбце
	// (amnezia-vpn-server-3wbe). Это НЕ то же самое, что нули в скорости:
	// плеер, добирающий буфер, двадцать секунд не получает ничего и при
	// этом прекрасно на связи. Отличить одно от другого по объёму трафика
	// нельзя вовсе — здесь это видно по возрасту рукопожатия.
	//
	// null значит «неизвестно», а не «был на связи»: столбцы, собранные из
	// записей прежнего формата, признака не несут.
	Online []*bool `json:"online"`
}

func (s *Server) apiClientSpeed(w http.ResponseWriter, r *http.Request) {
	id, err := parseClientID(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return
	}
	c, err := db.ClientByID(s.db(), id)
	if err != nil {
		// Чужой id и сломанная база отвечают одинаково коротко: наружу
		// уходит «нет такого», причина остаётся в журнале.
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "message": flashNotFound})
		return
	}

	window := r.URL.Query().Get("window")
	if window != speedWindowDay {
		window = speedWindow10Min
	}
	span := speed10MinSpan
	if window == speedWindowDay {
		span = 24 * time.Hour
	}
	columns := speedDefaultColumns
	if raw := r.URL.Query().Get("columns"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			columns = min(n, speedMaxColumns)
		}
	}

	to := time.Now().UTC().Truncate(time.Second)
	from := to.Add(-span)
	series, err := status.ReadSpeedSeries(
		filepath.Join(s.statusDir(), "speed.log"),
		status.SpeedKey(c.PublicKey), from, to, columns,
	)
	if err != nil {
		internalFailure(w, r, s, "api client speed", err)
		return
	}
	writeJSON(w, http.StatusOK, speedSeriesJSON(window, series))
}

func speedSeriesJSON(window string, series *status.SpeedSeries) speedJSON {
	out := speedJSON{
		Window:  window,
		FromUTC: series.FromUTC.Format(time.RFC3339),
		ToUTC:   series.ToUTC.Format(time.RFC3339),
		DownMin: make([]*uint64, len(series.Columns)),
		DownMax: make([]*uint64, len(series.Columns)),
		UpMin:   make([]*uint64, len(series.Columns)),
		UpMax:   make([]*uint64, len(series.Columns)),
		Online:  make([]*bool, len(series.Columns)),
	}
	for i, col := range series.Columns {
		if col.HasLiveness {
			// Отдельно от HasData: про связь бывает известно и там, где
			// скорости нет, — именно этот случай и интересен.
			out.Online[i] = &series.Columns[i].Online
		}
		if !col.HasData {
			continue
		}
		out.DownMin[i] = &series.Columns[i].DownMin
		out.DownMax[i] = &series.Columns[i].DownMax
		out.UpMin[i] = &series.Columns[i].UpMin
		out.UpMax[i] = &series.Columns[i].UpMax
	}
	return out
}
