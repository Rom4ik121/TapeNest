package catalog

import (
	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/streaming-service/internal/domain"
)

// Build is the fictional TapeNest catalog (the same titles the CineNest mocks showed).
// Nothing here is a real film and no magnet is attached, so playback never downloads a movie.
func Build() []domain.Title {
	const gb = 1024 * 1024 * 1024
	out := make([]domain.Title, 0, len(raw))
	for i, r := range raw {
		id := uuid.MustParse(UUIDFromSeed("title:" + itoa(i)))
		min := r.min
		t := domain.Title{
			Summary: domain.Summary{
				ID: id, Kind: r.kind, Title: r.title, OriginalTitle: r.original,
				Year: &r.year, Rating: &r.rating, Genres: append([]string(nil), r.genres...),
				Sort: i,
			},
			Description: "Вымышленное название каталога TapeNest. Это только метаданные: файл фильма не хранится и не скачивается. Fictional TapeNest title — metadata only.",
			RuntimeMin:  &min,
		}
		t.Files = filesFor(id, r.kind, min, strOr(r.original, r.title), gb)
		out = append(out, t)
	}
	return out
}

func strOr(p *string, fallback string) string {
	if p != nil && *p != "" {
		return *p
	}
	return fallback
}

func filesFor(id uuid.UUID, kind domain.TitleKind, min int, name string, gb int64) []domain.File {
	slug := stringsReplace(name)
	dur := float64(min * 60)
	if kind == domain.KindMovie {
		qs := []struct {
			q  string
			gb float64
		}{{"720p", 1.4}, {"1080p", 3.2}, {"2160p", 14.8}}
		out := make([]domain.File, 0, len(qs))
		for i, q := range qs {
			d := dur
			out = append(out, domain.File{
				ID:          uuid.MustParse(UUIDFromSeed(id.String() + ":" + q.q)),
				Name:        slug + "." + q.q + ".mkv",
				Quality:     q.q,
				SizeBytes:   int64(q.gb * float64(gb) * (float64(min) / 120)),
				DurationSec: &d,
				Sort:        i,
			})
		}
		return out
	}
	var out []domain.File
	n := 0
	for s := 1; s <= 2; s++ {
		for e := 1; e <= 6; e++ {
			for _, q := range []struct {
				q  string
				gb float64
			}{{"720p", 0.6}, {"1080p", 1.3}} {
				ss, ee := s, e
				d := dur
				out = append(out, domain.File{
					ID:          uuid.MustParse(UUIDFromSeed(id.String() + ":s" + itoa(s) + "e" + itoa(e) + ":" + q.q)),
					Name:        slug + ".S0" + itoa(s) + "E0" + itoa(e) + "." + q.q + ".mkv",
					Season:      &ss,
					Episode:     &ee,
					Quality:     q.q,
					SizeBytes:   int64(q.gb * float64(gb) * (float64(min) / 50)),
					DurationSec: &d,
					Sort:        n,
				})
				n++
			}
		}
	}
	return out
}

func stringsReplace(name string) string {
	b := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		if name[i] == ' ' {
			b = append(b, '.')
		} else {
			b = append(b, name[i])
		}
	}
	return string(b)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [8]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}

type row struct {
	title    string
	original *string
	kind     domain.TitleKind
	year     int
	rating   float64
	genres   []string
	min      int
}

func s(v string) *string { return &v }

var raw = []row{
	{"Янтарный горизонт", s("Amber Horizon"), domain.KindMovie, 2024, 7.8, []string{"drama", "adventure"}, 128},
	{"Тихая гавань", s("Quiet Harbor"), domain.KindSeries, 2023, 8.4, []string{"drama", "mystery"}, 48},
	{"Неоновый дождь", s("Neon Rain"), domain.KindMovie, 2022, 7.1, []string{"scifi", "thriller"}, 117},
	{"Последний маяк", s("The Last Lighthouse"), domain.KindMovie, 2021, 6.9, []string{"drama"}, 104},
	{"Станция Бирюза", s("Turquoise Station"), domain.KindSeries, 2024, 8.1, []string{"scifi"}, 52},
	{"Лисья тропа", s("Fox Trail"), domain.KindMovie, 2020, 7.4, []string{"family", "adventure"}, 96},
	{"Код полуночи", s("Midnight Code"), domain.KindSeries, 2022, 7.9, []string{"thriller", "crime"}, 45},
	{"Бумажные крылья", s("Paper Wings"), domain.KindMovie, 2019, 7.2, []string{"comedy", "drama"}, 101},
	{"Северный экспресс", s("Northern Express"), domain.KindMovie, 2023, 6.8, []string{"action"}, 112},
	{"Сад камней", s("Stone Garden"), domain.KindSeries, 2021, 8.7, []string{"drama"}, 58},
	{"Пурпурный рассвет", s("Purple Dawn"), domain.KindMovie, 2024, 7.0, []string{"fantasy"}, 133},
	{"Город шёпотов", s("City of Whispers"), domain.KindSeries, 2020, 7.6, []string{"mystery", "crime"}, 50},
	{"Двойной узел", s("Double Knot"), domain.KindMovie, 2018, 6.5, []string{"comedy"}, 94},
	{"Глубина", s("The Depth"), domain.KindMovie, 2022, 7.7, []string{"thriller", "scifi"}, 109},
	{"Кассетное лето", s("Cassette Summer"), domain.KindSeries, 2023, 8.0, []string{"comedy", "drama"}, 32},
	{"Мост через туман", s("Bridge Through Fog"), domain.KindMovie, 2021, 7.3, []string{"drama", "romance"}, 118},
	{"Ржавые звёзды", s("Rusty Stars"), domain.KindMovie, 2020, 6.7, []string{"scifi", "action"}, 125},
	{"Дом на холме", s("House on the Hill"), domain.KindSeries, 2024, 7.5, []string{"horror", "mystery"}, 44},
	{"Ветер перемен", s("Wind of Change"), domain.KindMovie, 2017, 7.9, []string{"drama"}, 140},
	{"Маленький оркестр", s("Little Orchestra"), domain.KindMovie, 2023, 8.2, []string{"family", "music"}, 99},
	{"Архив 47", s("Archive 47"), domain.KindSeries, 2022, 7.2, []string{"thriller"}, 47},
	{"Солёный ветер", s("Salt Wind"), domain.KindMovie, 2019, 6.9, []string{"adventure"}, 107},
	{"Полярная ночь", s("Polar Night"), domain.KindMovie, 2024, 7.6, []string{"thriller", "drama"}, 121},
	{"Шахматист", s("The Chess Player"), domain.KindSeries, 2021, 8.5, []string{"drama"}, 55},
	{"Облачный атлас снов", s("Cloud Atlas of Dreams"), domain.KindMovie, 2022, 7.0, []string{"fantasy", "drama"}, 136},
	{"Огни порта", s("Harbor Lights"), domain.KindMovie, 2020, 6.6, []string{"romance"}, 98},
	{"Эхо", s("Echo"), domain.KindSeries, 2023, 7.8, []string{"scifi", "mystery"}, 49},
	{"Медленный вальс", s("Slow Waltz"), domain.KindMovie, 2018, 7.4, []string{"romance", "music"}, 102},
}
