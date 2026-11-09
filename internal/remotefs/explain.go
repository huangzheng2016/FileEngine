package remotefs

import (
	"errors"
	"strings"
)

type hint struct {
	needles []string
	text    string
}

// Ordered: authentication and share problems are checked before the generic
// network ones, because their messages often mention the dial too.
var connHints = []hint{
	{[]string{"unable to authenticate", "logon is invalid", "logon_failure", "530 ", "login incorrect", "authentication failed"},
		"用户名或密码不对：检查用户名和密码（或私钥）"},
	{[]string{"account is currently disabled", "account_disabled", "password_expired"},
		"账号已停用或密码已过期：在 NAS 上检查这个账号"},
	{[]string{"network name not found", "bad_network_name"},
		"找不到这个共享文件夹：检查共享名，或回到上一步从列表里选"},
	{[]string{"mnt3err_acces", "mnt3err_perm"},
		"NAS 拒绝挂载：在 NAS 的 NFS 权限里允许本机 IP 访问这个共享"},
	{[]string{"mnt3err_noent"},
		"NFS 导出路径不存在：在 NAS 的 NFS 设置里查看导出路径"},
	{[]string{"access denied", "permission denied", "access_denied", "550 "},
		"没有访问权限：换一个有权限的账号或目录"},
	{[]string{"no such file", "not exist", "object_name_not_found", "object_path_not_found", "not found"},
		"目录不存在：检查路径是否写对"},
	{[]string{"connection refused", "actively refused"},
		"对方拒绝连接：检查端口是否正确、对应服务是否已开启"},
	{[]string{"no such host", "server misbehaving", "name resolution"},
		"找不到这个地址：检查主机名或 IP 是否写对"},
	{[]string{"no route to host", "network is unreachable", "host is down"},
		"网络不通：检查地址，确认本机和对方在同一网络"},
	{[]string{"connection reset", "broken pipe", "eof"},
		"连接被对方断开：检查端口是否对应这种连接方式"},
}

// Explain turns a connection error into one plain sentence with the next step
// to take; the raw error stays available to the caller as detail.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	var mm *HostKeyMismatchError
	switch {
	case errors.As(err, &mm):
		return err.Error()
	case errors.Is(err, ErrLocalDisabled), errors.Is(err, ErrKeyInvalid),
		errors.Is(err, ErrKeyPassphraseMissing), errors.Is(err, ErrKeyPassphraseWrong):
		return err.Error()
	case IsTimeout(err):
		return "连接超时：检查地址和端口，确认对方开机且网络可达"
	}
	msg := strings.ToLower(err.Error())
	for _, h := range connHints {
		for _, n := range h.needles {
			if strings.Contains(msg, n) {
				return h.text
			}
		}
	}
	return "连接失败：" + err.Error()
}
