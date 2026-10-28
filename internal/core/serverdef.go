package core

import (
        "fmt"
)

// ServerDef describes a managed SimpleX server component.
type ServerDef struct {
        Kind        Kind
        Name        string // display name
        Description string
        Repo        string // github repo "simplex-chat/simplexmq"
        // AssetPrefix matches GitHub release assets, e.g. "smp-server-ubuntu".
        AssetPrefix  string
        IniName      string // config file name inside CfgDir
        CfgEnvVar    string
        LogEnvVar    string
        ControlProto string // "smp" | "xftp" | "ntf" | ""
}

var defs = map[Kind]ServerDef{
        KindSMP: {
                Kind:         KindSMP,
                Name:         "SMP server",
                Description:  "Message relay server (simplex queues) - the core of SimpleX messaging",
                Repo:         "simplex-chat/simplexmq",
                AssetPrefix:  "smp-server-ubuntu",
                IniName:      "smp-server.ini",
                CfgEnvVar:    "SMP_SERVER_CFG_PATH",
                LogEnvVar:    "SMP_SERVER_LOG_PATH",
                ControlProto: "smp",
        },
        KindXFTP: {
                Kind:         KindXFTP,
                Name:         "XFTP server",
                Description:  "File transfer relay server for sending files in chats",
                Repo:         "simplex-chat/simplexmq",
                AssetPrefix:  "xftp-server-ubuntu",
                IniName:      "file-server.ini", // the binary identifies as "file-server"
                CfgEnvVar:    "XFTP_SERVER_CFG_PATH",
                LogEnvVar:    "XFTP_SERVER_LOG_PATH",
                ControlProto: "xftp",
        },
        KindNTF: {
                Kind:         KindNTF,
                Name:         "NTF server",
                Description:  "Push notification server (advanced - requires PostgreSQL)",
                Repo:         "simplex-chat/simplexmq",
                AssetPrefix:  "ntf-server-ubuntu",
                IniName:      "ntf-server.ini",
                CfgEnvVar:    "NTF_SERVER_CFG_PATH",
                LogEnvVar:    "NTF_SERVER_LOG_PATH",
                ControlProto: "ntf",
        },
        KindRelay: {
                Kind:         KindRelay,
                Name:         "Chat relay",
                Description:  "simplex-chat CLI in --relay mode: delivers channel messages, has its own address",
                Repo:         "simplex-chat/simplex-chat",
                AssetPrefix:  "simplex-chat-ubuntu",
                IniName:      "",
                CfgEnvVar:    "",
                LogEnvVar:    "",
                ControlProto: "",
        },
}

// simplexmqSourceRepo is the server source code URI required by AGPLv3
// and shown on the SMP and XFTP info web pages (the [INFORMATION]
// source_code key). sxops installs the official, unmodified simplexmq
// binaries, so the upstream repository is the correct source. The SMP
// init command gets it via its --source-code flag; the XFTP init
// command has no such flag (it only leaves the key commented out), so
// the portable profile patches it into the INI (without it the XFTP
// info page shows no information section at all). The NTF server does
// not read the INFORMATION section and is skipped.
const simplexmqSourceRepo = "https://github.com/simplex-chat/simplexmq"

// Def returns the ServerDef for a kind.
func Def(k Kind) ServerDef { return defs[k] }

// QueuesTitle is the admin-panel title for the moderation target list.
func (d ServerDef) QueuesTitle() string {
        switch d.Kind {
        case KindSMP:
                return "Chat queues on this server"
        case KindXFTP:
                return "Files on this server"
        }
        return ""
}

// DefaultPort returns the transport port used by the portable profile.
func DefaultPort(k Kind) int {
        switch k {
        case KindSMP:
                return 5223
        case KindXFTP:
                return 5225
        case KindNTF:
                return 5227
        case KindRelay:
                return 0
        }
        return 0
}

// DefaultControlPort returns the control port used by the portable profile.
func DefaultControlPort(k Kind) int {
        switch k {
        case KindSMP:
                return 5224
        case KindXFTP:
                return 5226
        case KindNTF:
                return 5228
        }
        return 0
}

// DefaultWebPort returns the [WEB] http info-page port (0 = disabled).
func DefaultWebPort(k Kind) int {
        switch k {
        case KindSMP:
                return 8023
        case KindXFTP:
                return 8025
        case KindNTF:
                return 8027
        }
        return 0
}

// DefaultWebHttpsPort returns the local port of the SMP server's
// embedded HTTPS web page ([WEB] https - the invite/link landing page a
// browser gets for https:// links; 0 = only the SMP server has one).
// A high port is used because the unprivileged server cannot bind 443;
// the onion service maps its virtual port 443 (what browsers connect
// to) onto this local port, and a public-host deployment forwards the
// router's 443 to it.
func DefaultWebHttpsPort(k Kind) int {
        if k == KindSMP {
                return 8443
        }
        return 0
}

// AssetName picks the release asset for the current platform.
//
// simplexmq publishes Ubuntu 22.04/24.04 builds for x86-64 and aarch64
// (hyphenated arch); the chat CLI uses x86_64 (underscore) on Ubuntu.
func (d ServerDef) AssetName(goos, goarch string) (string, error) {
        if goos != "linux" {
                return "", fmt.Errorf("unsupported OS %s", goos)
        }
        hyphen := "x86-64"
        underscore := "x86_64"
        if goarch == "arm64" || goarch == "aarch64" {
                hyphen = "aarch64"
                underscore = "aarch64"
        } else if goarch != "amd64" {
                return "", fmt.Errorf("unsupported arch %s", goarch)
        }
        if d.Kind == KindRelay {
                return fmt.Sprintf("%s-24_04-%s", d.AssetPrefix, underscore), nil
        }
        return fmt.Sprintf("%s-24_04-%s", d.AssetPrefix, hyphen), nil
}

// InitArgs builds the non-interactive `init` command for a server kind
// under the portable profile.
func InitArgs(k Kind, s Settings, paths *Paths) ([]string, error) {
        switch k {
        case KindSMP:
                args := []string{"init", "-y", "--source-code"}
                if s.PasswordProtect {
                        args = append(args, "--password", s.QueuePassword)
                } else {
                        args = append(args, "--no-password")
                }
                args = append(args, "--control-port") // enables 5224 + token pair
                // --socks-proxy matches the official init recommendation;
                // it only affects dialing .onion routing targets.
                args = append(args, "--socks-proxy")
                args = append(args, "--disable-web", "--ip", "127.0.0.1")
                if s.PublicHost != "" {
                        args = append(args, "--fqdn", s.PublicHost)
                }
                return args, nil
        case KindXFTP:
                // init REQUIRES --quota (no default); the value only seeds the INI
                // template - applyPortableProfile rewrites or comments the key out
                // right after, so a fixed placeholder is used when the quota is off.
                q := xftpQuotaGB(s)
                if q <= 0 {
                        q = 10
                }
                args := []string{"init", "--path", paths.FilesDir(), "--quota", fmt.Sprintf("%dgb", q)}
                if s.PasswordProtect {
                        // xftp init has no --password flag; the password is patched
                        // into the INI afterwards by the provisioner.
                        _ = s.QueuePassword
                }
                args = append(args, "--ip", "127.0.0.1")
                if s.PublicHost != "" {
                        args = append(args, "--fqdn", s.PublicHost)
                }
                return args, nil
        case KindNTF:
                args := []string{"init", "--ip", "127.0.0.1"}
                if s.PublicHost != "" {
                        args = append(args, "--fqdn", s.PublicHost)
                }
                return args, nil
        case KindRelay:
                return nil, nil // no init command
        }
        return nil, fmt.Errorf("unknown kind %s", k)
}

// xftpQuotaGB returns the configured file storage quota in GB
// (0 = off / no limit - the file server INI then carries no
// storage_quota key at all, which upstream reads as "unlimited").
func xftpQuotaGB(s Settings) int {
        if s.XFTPQuotaGB < 0 {
                return 0
        }
        return s.XFTPQuotaGB
}

// xftpExpireHours returns the effective XFTP file expiry in hours
// (files are deleted from the server after that time). 48h is the
// upstream default (defFileExpirationHours).
func xftpExpireHours(s Settings) int {
        if s.XFTPExpireHours <= 0 {
                return 48
        }
        return s.XFTPExpireHours
}

// StartArgs returns the argv used to run the server ("start" for
// simplexmq servers; the relay CLI is special-cased by the supervisor).
func StartArgs(k Kind) []string {
        if k == KindRelay {
                return nil
        }
        return []string{"start"}
}
