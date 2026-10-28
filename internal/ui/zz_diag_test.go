package ui

// TEMPORARY diagnostic (not part of any deliverable): renders the XFTP
// file list through the real renderQueues with real-world FNEW records
// and dumps the widget texts, to see exactly what a user sees in the
// "when" column.

import (
        "fmt"
        "testing"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/test"
        "fyne.io/fyne/v2/container"
        "fyne.io/fyne/v2/widget"

        "sxops/internal/core"
)

func dumpTree(o fyne.CanvasObject, depth int) {
        pad := ""
        for i := 0; i < depth; i++ {
                pad += "  "
        }
        switch x := o.(type) {
        case *widget.Label:
                fmt.Printf("%sLABEL %q\n", pad, x.Text)
        case *widget.Button:
                fmt.Printf("%sBUTTON %q\n", pad, x.Text)
        case *fyne.Container:
                fmt.Printf("%sCONTAINER(%d)\n", pad, len(x.Objects))
                for _, ch := range x.Objects {
                        dumpTree(ch, depth+1)
                }
        default:
                fmt.Printf("%s%T\n", pad, o)
        }
}

func TestXFTPListRenderDiagnostic(t *testing.T) {
        a := test.NewApp()
        w := a.NewWindow("diag")
        c := &console{app: a, win: w, glog: &guiLog{}}
        v := &adminView{c: c, selected: core.KindXFTP}
        v.queuesHead = widget.NewLabel("")
        v.queuesCnt = widget.NewLabel("")
        v.queuesCnt.TextStyle = fyne.TextStyle{Italic: true}
        v.queuesBox = container.NewVBox()
        v.banIDEntry = widget.NewEntry()

        // real-world FNEW records (from the running test server log):
        // FNEW <sId> <sndKey> <size> <digest> <createdAt> [status]
        // FADD <sId> <rId>:<rKey>
        log := "FNEW sid111111111111111111111111 MCowBQYDK2VwAyEAGFeHWezFp9n7jprXjV3h_toz90fJA_-VTcJCEgbbOsE= 102400 cfNv5lCGFwz9QNlNA8tVzMbvMfcfvTReFsHdo2HtjHI= 1792890000 active\n" +
                "FADD sid111111111111111111111111 rid111111111111111111111111:MCowBQYDK2VuAyEAYo3UztgGTRwq27Pmb-qCmB24ksJhBEtRViM2eDsNydE=\n"
        rows := core.ParseStoreLog(core.KindXFTP, []byte(log))
        if len(rows) != 1 {
                t.Fatalf("expected 1 row, got %d", len(rows))
        }
        for _, r := range rows {
                t.Logf("row: ID=%s CreatedAt=%q SeenAt=%q FileSize=%s FileID=%s",
                        r.ID, r.CreatedAt, r.SeenAt, r.FileSize, r.FileID)
                d, hm := queueWhenParts(core.KindXFTP, r)
                t.Logf("queueWhenParts -> day=%q hm=%q ; queueWhen=%q", d, hm, queueWhen(core.KindXFTP, r))
        }

        v.renderQueues(rows)
        w.SetContent(v.queuesBox)
        w.ShowAndRun()
        fmt.Println("=== queuesCnt:", v.queuesCnt.Text)
        dumpTree(v.queuesBox, 0)
}
