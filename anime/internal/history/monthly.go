package history

import "time"

// Month is what was watched in Aniko in a month: the episodes finished
// (watched past finished of their length), and the minutes watched (what was
// seen of each). An episode counts in the month it was last watched in (the
// history keeps one position per episode).
type Month struct {
	Month    string `json:"month"` // "2026-09"
	Episodes int    `json:"episodes"`
	Minutes  int    `json:"minutes"`
}

// Monthly is the last n months (this one included, the oldest first), in
// loc's calendar.
func (s *Store) Monthly(n int, finished float64, now time.Time, loc *time.Location) ([]Month, error) {
	now = now.In(loc)
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, -(n - 1), 0)
	out := make([]Month, n)
	index := map[string]int{}
	for i := range out {
		m := first.AddDate(0, i, 0).Format("2006-01")
		out[i].Month = m
		index[m] = i
	}
	rows, err := s.db.Query(`SELECT position, duration, updated_at FROM watch_history WHERE updated_at >= ?`, first.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	minutes := make([]float64, n)
	for rows.Next() {
		var pos, dur float64
		var at int64
		if err := rows.Scan(&pos, &dur, &at); err != nil {
			return nil, err
		}
		i, ok := index[time.Unix(at, 0).In(loc).Format("2006-01")]
		if !ok {
			continue
		}
		if dur > 0 && pos/dur >= finished {
			out[i].Episodes++
			minutes[i] += dur / 60
		} else {
			minutes[i] += pos / 60
		}
	}
	for i := range out {
		out[i].Minutes = int(minutes[i] + 0.5)
	}
	return out, rows.Err()
}
