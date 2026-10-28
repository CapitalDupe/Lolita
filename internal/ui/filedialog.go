package ui

import (
        "log"
        "strings"

        "fyne.io/fyne/v2"
        "fyne.io/fyne/v2/dialog"
)

// showSaveDialogQuiet opens Fyne's file-save dialog with the harmless
// favorites log noise muffled (v1.6.46).
//
// On installs without a complete ~/.config/user-dirs.dirs (fresh
// servers, minimal WSL images) xdg-user-dir answers $HOME for the six
// standard folders, and every Fyne file dialog then logs
//
//      Fyne error:  Getting favorite locations
//        Cause: this computer does not define a VIDEOS folder
//        At: .../fyne/v2/dialog/file.go:400
//
// to stderr (fyne v2.8.2 dialog/file.go loadFavorites). The dialog
// itself works fine - only the sidebar shows fewer shortcuts. v1.6.42
// silenced this by creating the missing folders in $HOME; the operator
// asked for no folder side effects, so the noise is now swallowed
// instead (muffleFyneLogNoise) and $HOME stays untouched.
func showSaveDialogQuiet(parent fyne.Window, fileName string, onSave func(fyne.URIWriteCloser, error)) {
        fd := dialog.NewFileSave(onSave, parent)
        fd.SetFileName(fileName)
        muffleFyneLogNoise(fd.Show)
}

// favoritesNoiseMarker matches the header line of the log block Fyne's
// file dialog prints when a standard user folder is undefined.
const favoritesNoiseMarker = "Getting favorite locations"

// dropFavoritesNoise selects those favorites blocks.
func dropFavoritesNoise(header string) bool {
        return strings.Contains(header, favoritesNoiseMarker)
}

// muffleFyneLogNoise runs fn with the file dialog's favorites noise
// muffled: everything captured while fn runs is re-printed to the
// original log writer EXCEPT the favorites blocks. fn must be
// synchronous (fd.Show is: the dialog, its favorites and the initial
// directory listing are built inline).
func muffleFyneLogNoise(fn func()) {
        prev := log.Writer()
        captured := withLogCaptured(fn)
        reprintExceptBlocks(captured, prev, dropFavoritesNoise)
}
