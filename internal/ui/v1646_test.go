package ui

// v1.6.46 tests: the "no side effects on the user's home" fix the
// operator asked for, plus a pin on the one that was reversed:
//  1. the "Save log to a file" dialog no longer creates the standard
//     XDG folders (Desktop, Documents, Downloads, Music, Pictures,
//     Videos) in $HOME and no longer writes ~/.config/user-dirs.dirs -
//     the v1.6.42 ensureXDGUserDirs approach is gone entirely. The
//     harmless "Getting favorite locations" log noise that the folders
//     used to silence is muffled while the dialog is built instead
//     (filedialog.go);
//  2. fyne's empty ~/.cache/fyne/sxops tree is LEFT ALONE. A
//     redirect-and-sweep suppression was built first, the operator
//     reviewed it and reversed it ("dont do anything there"), so the
//     app is created with plain stock app.NewWithID and nothing in
//     sxops touches the cache location any more (source-scan pins
//     below keep it that way).

import (
        "bytes"
        "log"
        "os"
        "strings"
        "testing"
)

// TestNoXDGFolderCreationV1646: no trace of the folder-creating fix
// survives - the whole xdguserdirs.go file is gone and nothing calls
// into it any more (source-scan pattern, like the v1.6.21+ pins).
func TestNoXDGFolderCreationV1646(t *testing.T) {
        if _, err := os.Stat("xdguserdirs.go"); err == nil {
                t.Errorf("xdguserdirs.go still exists - the XDG folder creation must be gone")
        }
        src, err := os.ReadFile("logs.go")
        if err != nil {
                t.Skipf("source not readable: %v", err)
        }
        s := string(src)
        for _, gone := range []string{"ensureXDGUserDirs", "xdgDirNames", "user-dirs.dirs"} {
                if strings.Contains(s, gone) {
                        t.Errorf("logs.go must no longer reference %q", gone)
                }
        }
        if !strings.Contains(s, "showSaveDialogQuiet") {
                t.Errorf("saveLog must open the dialog through showSaveDialogQuiet (muffled noise):\n%s", s)
        }
}

// TestReprintDropsFavoritesNoiseV1646 pins the noise filter: the
// favorites block disappears while everything else survives verbatim -
// including a real error's own "  Cause:" line and lines logged after
// the block.
func TestReprintDropsFavoritesNoiseV1646(t *testing.T) {
        in := "2026/10/28 Fyne error:  Getting favorite locations\n" +
                "2026/10/28   Cause: this computer does not define a VIDEOS folder\n" +
                "2026/10/28   At: /fyne/v2/dialog/file.go:400\n" +
                "2026/10/28 Fyne error:  Unable to read ListableURI\n" +
                "2026/10/28   Cause: permission denied\n"
        var out bytes.Buffer
        reprintExceptBlocks(in, &out, dropFavoritesNoise)
        got := out.String()
        if strings.Contains(got, "Getting favorite locations") || strings.Contains(got, "VIDEOS") {
                t.Errorf("favorites noise was not dropped:\n%s", got)
        }
        for _, want := range []string{"Unable to read ListableURI", "permission denied"} {
                if !strings.Contains(got, want) {
                        t.Errorf("real error line %q was swallowed:\n%s", want, got)
                }
        }
}

// TestReprintNoiseOnlyV1646: a capture that holds nothing but the noise
// block produces no output at all.
func TestReprintNoiseOnlyV1646(t *testing.T) {
        in := "Fyne error:  Getting favorite locations\n" +
                "  Cause: this computer does not define a DESKTOP folder\n" +
                "  At: file.go:400\n"
        var out bytes.Buffer
        reprintExceptBlocks(in, &out, dropFavoritesNoise)
        if out.Len() != 0 {
                t.Errorf("noise-only capture produced output:\n%s", out.String())
        }
}

// TestMuffleFyneLogNoiseV1646: end to end - the std log is redirected
// around fn and restored afterwards, the noise block vanishes and any
// real line logged inside the window still reaches the original writer.
func TestMuffleFyneLogNoiseV1646(t *testing.T) {
        var out bytes.Buffer
        prev := log.Writer()
        log.SetOutput(&out)
        defer log.SetOutput(prev)

        muffleFyneLogNoise(func() {
                log.Println("Fyne error:  Getting favorite locations")
                log.Println("  Cause: this computer does not define a VIDEOS folder")
                log.Println("Fyne error:  Something the operator should see")
        })

        if log.Writer() != &out {
                t.Errorf("log output was not restored after the muffler ran")
        }
        s := out.String()
        if strings.Contains(s, "VIDEOS") {
                t.Errorf("noise reached the original writer:\n%s", s)
        }
        if !strings.Contains(s, "Something the operator should see") {
                t.Errorf("real line was lost inside the muffle window:\n%s", s)
        }
}

// TestFyneCacheLeftAloneV1646 pins the reversal of the cache
// suppression: nothing in the ui package redirects, creates or sweeps
// fyne's cache location any more - the app is built with plain stock
// app.NewWithID and the (empty) ~/.cache/fyne/sxops tree fyne makes is
// simply left alone (source-scan over every non-test ui source, so a
// reintroduction in ANY file trips it, not just app.go).
func TestFyneCacheLeftAloneV1646(t *testing.T) {
        if _, err := os.Stat("fynecache.go"); err == nil {
                t.Errorf("fynecache.go still exists - the cache suppression must be gone")
        }
        entries, err := os.ReadDir(".")
        if err != nil {
                t.Skipf("ui sources not readable: %v", err)
        }
        gone := []string{
                "newAppWithoutCacheDir",
                "removeEmptyFyneCache",
                "dropCacheSpaceNoise",
                "XDG_CACHE_HOME",
        }
        for _, e := range entries {
                name := e.Name()
                if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
                        continue
                }
                src, err := os.ReadFile(name)
                if err != nil {
                        continue
                }
                s := string(src)
                for _, g := range gone {
                        if strings.Contains(s, g) {
                                t.Errorf("%s must no longer reference %q", name, g)
                        }
                }
        }
        appSrc, err := os.ReadFile("app.go")
        if err != nil {
                t.Skipf("app.go not readable: %v", err)
        }
        if !strings.Contains(string(appSrc), "app.NewWithID(\"sxops\")") {
                t.Errorf("Run must create the app with plain stock app.NewWithID")
        }
}
