package core

import (
        "encoding/csv"
        "io"
        "os"
        "path/filepath"
        "sort"
        "strconv"
        "strings"
        "time"
)

// DailyStat is one day of the smp-server daily statistics CSV.
type DailyStat struct {
        Day            time.Time
        QCreated       int
        QSecured       int
        QDeleted       int
        MsgSent        int
        MsgRecv        int
        DayMsgQueues   int
        WeekMsgQueues  int
        MonthMsgQueues int
        QCount         int
        MsgCount       int
}

// StatsDailyLogName is the daily statistics CSV file a server writes
// into its LOG directory when [STORE_LOG] log_stats = on. Names as
// hardcoded upstream (simplexmq v6.5.0, stable): smp and nft servers
// write <name>-server-stats.daily.log (Server/Main.hs:577,
// Notifications/Server/Main.hs:215), the file server writes
// file-server-stats.daily.log (FileTransfer/Server/Main.hs:311).
// The relay is not a simplexmq server and has none ("").
func StatsDailyLogName(kind Kind) string {
        switch kind {
        case KindSMP:
                return "smp-server-stats.daily.log"
        case KindXFTP:
                return "file-server-stats.daily.log"
        case KindNTF:
                return "ntf-server-stats.daily.log"
        }
        return ""
}

// StatsHistory reads the daily stats CSV produced by log_stats = on.
// Field order (v6.5.0): fromTime,qCreated,qSecured,qDeleted,msgSent,msgRecv,
// dayMsgQueues,weekMsgQueues,monthMsgQueues,...
func StatsHistory(kind Kind, logDir string) ([]DailyStat, error) {
        name := StatsDailyLogName(kind)
        if name == "" {
                return nil, nil
        }
        path := filepath.Join(logDir, name)
        f, err := os.Open(path)
        if err != nil {
                if os.IsNotExist(err) {
                        return nil, nil
                }
                return nil, err
        }
        defer f.Close()
        // Some installs prefix the header with fromTime.
        r := csv.NewReader(f)
        r.FieldsPerRecord = -1
        var out []DailyStat
        for {
                rec, err := r.Read()
                if err == io.EOF {
                        break
                }
                if err != nil || len(rec) < 7 {
                        continue
                }
                day, err := time.Parse("2006-01-02", strings.TrimSpace(rec[0]))
                if err != nil {
                        continue
                }
                n := func(i int) int {
                        if i >= len(rec) {
                                return 0
                        }
                        v, _ := strconv.Atoi(strings.TrimSpace(rec[i]))
                        return v
                }
                out = append(out, DailyStat{
                        Day:            day,
                        QCreated:       n(1),
                        QSecured:       n(2),
                        QDeleted:       n(3),
                        MsgSent:        n(4),
                        MsgRecv:        n(5),
                        DayMsgQueues:   n(6),
                        WeekMsgQueues:  n(7),
                        MonthMsgQueues: n(8),
                        QCount:         0,
                        MsgCount:       0,
                })
        }
        sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
        return out, nil
}
