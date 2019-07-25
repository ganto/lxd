// +build linux
// +build cgo

package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/netutils"
	"github.com/lxc/lxd/shared/osarch"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <elf.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/seccomp.h>
#include <linux/types.h>
#include <linux/kdev_t.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/sysmacros.h>
#include <sys/types.h>
#include <unistd.h>

#ifndef SECCOMP_GET_NOTIF_SIZES
#define SECCOMP_GET_NOTIF_SIZES 3
#endif

#ifndef SECCOMP_RET_USER_NOTIF
#define SECCOMP_RET_USER_NOTIF 0x7fc00000U

struct seccomp_notif_sizes {
	__u16 seccomp_notif;
	__u16 seccomp_notif_resp;
	__u16 seccomp_data;
};

struct seccomp_notif {
	__u64 id;
	__u32 pid;
	__u32 flags;
	struct seccomp_data data;
};

struct seccomp_notif_resp {
	__u64 id;
	__s64 val;
	__s32 error;
	__u32 flags;
};

#endif // !SECCOMP_RET_USER_NOTIF

struct seccomp_notif_sizes expected_sizes;

struct seccomp_notify_proxy_msg {
	uint64_t __reserved;
	pid_t monitor_pid;
	pid_t init_pid;
	struct seccomp_notif_sizes sizes;
	uint64_t cookie_len;
	// followed by: seccomp_notif, seccomp_notif_resp, cookie
};

#define SECCOMP_PROXY_MSG_SIZE (sizeof(struct seccomp_notify_proxy_msg))
#define SECCOMP_NOTIFY_SIZE (sizeof(struct seccomp_notif))
#define SECCOMP_RESPONSE_SIZE (sizeof(struct seccomp_notif_resp))
#define SECCOMP_MSG_SIZE_MIN (SECCOMP_PROXY_MSG_SIZE + SECCOMP_NOTIFY_SIZE + SECCOMP_RESPONSE_SIZE)
#define SECCOMP_COOKIE_SIZE (64 * sizeof(char))
#define SECCOMP_MSG_SIZE_MAX (SECCOMP_MSG_SIZE_MIN + SECCOMP_COOKIE_SIZE)

#ifdef SECCOMP_RET_USER_NOTIF

static int seccomp_notify_get_sizes(struct seccomp_notif_sizes *sizes)
{
	if (syscall(SYS_seccomp, SECCOMP_GET_NOTIF_SIZES, 0, sizes) != 0)
		return -1;

	if (sizes->seccomp_notif != sizeof(struct seccomp_notif) ||
	    sizes->seccomp_notif_resp != sizeof(struct seccomp_notif_resp) ||
	    sizes->seccomp_data != sizeof(struct seccomp_data))
		return -1;

	return 0;
}

static int device_allowed(dev_t dev, mode_t mode)
{
	if ((dev == makedev(0, 0)) && (mode & S_IFCHR)) // whiteout
		return 0;

	if ((dev == makedev(5, 1)) && (mode & S_IFCHR)) // /dev/console
		return 0;

	if ((dev == makedev(1, 7)) && (mode & S_IFCHR)) // /dev/full
		return 0;

	if ((dev == makedev(1, 3)) && (mode & S_IFCHR)) // /dev/null
		return 0;

	if ((dev == makedev(1, 8)) && (mode & S_IFCHR)) // /dev/random
		return 0;

	if ((dev == makedev(5, 0)) && (mode & S_IFCHR)) // /dev/tty
		return 0;

	if ((dev == makedev(1, 9)) && (mode & S_IFCHR)) // /dev/urandom
		return 0;

	if ((dev == makedev(1, 5)) && (mode & S_IFCHR)) // /dev/zero
		return 0;

	return -EPERM;
}

#include <linux/audit.h>

struct lxd_seccomp_data_arch {
	int arch;
	int nr_mknod;
	int nr_mknodat;
};

// ordered by likelihood of usage...
static const struct lxd_seccomp_data_arch seccomp_notify_syscall_table[] = {
#ifdef AUDIT_ARCH_X86_64
	{ AUDIT_ARCH_X86_64,      133, 259 },
#endif
#ifdef AUDIT_ARCH_I386
	{ AUDIT_ARCH_I386,         14, 297 },
#endif
#ifdef AUDIT_ARCH_AARCH64
	{ AUDIT_ARCH_AARCH64,      -1,  33 },
#endif
#ifdef AUDIT_ARCH_ARM
	{ AUDIT_ARCH_ARM,          14, 324 },
#endif
#ifdef AUDIT_ARCH_ARMEB
	{ AUDIT_ARCH_ARMEB,        14, 324 },
#endif
#ifdef AUDIT_ARCH_S390
	{ AUDIT_ARCH_S390,         14, 290 },
#endif
#ifdef AUDIT_ARCH_S390X
	{ AUDIT_ARCH_S390X,        14, 290 },
#endif
#ifdef AUDIT_ARCH_PPC
	{ AUDIT_ARCH_PPC,          14, 288 },
#endif
#ifdef AUDIT_ARCH_PPC64
	{ AUDIT_ARCH_PPC64,        14, 288 },
#endif
#ifdef AUDIT_ARCH_PPC64LE
	{ AUDIT_ARCH_PPC64LE,      14, 288 },
#endif
#ifdef AUDIT_ARCH_SPARC
	{ AUDIT_ARCH_SPARC,        14, 286 },
#endif
#ifdef AUDIT_ARCH_SPARC64
	{ AUDIT_ARCH_SPARC64,      14, 286 },
#endif
#ifdef AUDIT_ARCH_MIPS
	{ AUDIT_ARCH_MIPS,         14, 290 },
#endif
#ifdef AUDIT_ARCH_MIPSEL
	{ AUDIT_ARCH_MIPSEL,       14, 290 },
#endif
#ifdef AUDIT_ARCH_MIPS64
	{ AUDIT_ARCH_MIPS64,      131, 249 },
#endif
#ifdef AUDIT_ARCH_MIPS64N32
	{ AUDIT_ARCH_MIPS64N32,   131, 253 },
#endif
#ifdef AUDIT_ARCH_MIPSEL64
	{ AUDIT_ARCH_MIPSEL64,    131, 249 },
#endif
#ifdef AUDIT_ARCH_MIPSEL64N32
	{ AUDIT_ARCH_MIPSEL64N32, 131, 253 },
#endif
};

static int seccomp_notify_mknod_set_response(int fd_mem,
					     struct seccomp_notif *req,
					     struct seccomp_notif_resp *resp,
					     char *buf, size_t size,
					     mode_t *mode, dev_t *dev,
					     pid_t *pid)
{
	int ret;
	ssize_t bytes;

	resp->id = req->id;
	resp->flags = req->flags;
	resp->val = 0;
	resp->error = 0;

	for (size_t i = 0; i < (sizeof(seccomp_notify_syscall_table) / sizeof(seccomp_notify_syscall_table[0])); i++) {
		const struct lxd_seccomp_data_arch *entry = &seccomp_notify_syscall_table[i];

		if (entry->arch != req->data.arch)
			continue;

		if (entry->nr_mknod == req->data.nr) {
			resp->error = device_allowed(req->data.args[2], req->data.args[1]);
			if (resp->error) {
				errno = EPERM;
				return -EPERM;
			}

			bytes = pread(fd_mem, buf, size, req->data.args[0]);
			if (bytes < 0)
				return -errno;

			*mode = req->data.args[1];
			*dev = req->data.args[2];
			*pid = req->pid;

			return 0;
		}

		if (entry->nr_mknodat == req->data.nr) {
			if ((int)req->data.args[0] != AT_FDCWD) {
				errno = EINVAL;
				return -EINVAL;
			}

			resp->error = device_allowed(req->data.args[3], req->data.args[2]);
			if (resp->error) {
				errno = EPERM;
				return -EPERM;
			}

			bytes = pread(fd_mem, buf, size, req->data.args[1]);
			if (bytes < 0)
				return -errno;

			*mode = req->data.args[2];
			*dev = req->data.args[3];
			*pid = req->pid;

			return 0;
		}

		break;
	}

	errno = EPERM;
	return -EPERM;
}

static void seccomp_notify_mknod_update_response(struct seccomp_notif_resp *resp,
						 int new_neg_errno)
{
	resp->error = new_neg_errno;
}

static void prepare_seccomp_iovec(struct iovec *iov,
				  struct seccomp_notify_proxy_msg *msg,
				  struct seccomp_notif *notif,
				  struct seccomp_notif_resp *resp, char *cookie)
{
	iov[0].iov_base = msg;
	iov[0].iov_len = SECCOMP_PROXY_MSG_SIZE;

	iov[1].iov_base = notif;
	iov[1].iov_len = SECCOMP_NOTIFY_SIZE;

	iov[2].iov_base = resp;
	iov[2].iov_len = SECCOMP_RESPONSE_SIZE;

	iov[3].iov_base = cookie;
	iov[3].iov_len = SECCOMP_COOKIE_SIZE;
}

#else

static void prepare_seccomp_iovec(struct iovec *iov,
				  struct seccomp_notify_proxy_msg *msg,
				  struct seccomp_notif *notif,
				  struct seccomp_notif_resp *resp, char *cookie)
{
}

static int seccomp_notify_get_sizes(struct seccomp_notif_sizes *sizes)
{
	errno = ENOSYS;
	return -1;
}

static int seccomp_notify_mknod_set_response(int fd_mem,
					     struct seccomp_notif *req,
					     struct seccomp_notif_resp *resp,
					     char *buf, size_t size,
					     mode_t *mode, dev_t *dev,
					     pid_t *pid)
{
	errno = ENOSYS;
	return -1;
}

static void seccomp_notify_mknod_update_response(struct seccomp_notify_proxy_msg *msg,
						 int new_neg_errno)
{
}
#endif // SECCOMP_RET_USER_NOTIF
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

const SECCOMP_HEADER = `2
`

const DEFAULT_SECCOMP_POLICY = `reject_force_umount  # comment this to allow umount -f;  not recommended
[all]
kexec_load errno 38
open_by_handle_at errno 38
init_module errno 38
finit_module errno 38
delete_module errno 38
`
const SECCOMP_NOTIFY_POLICY = `mknod notify [1,8192,SCMP_CMP_MASKED_EQ,61440]
mknod notify [1,24576,SCMP_CMP_MASKED_EQ,61440]
mknodat notify [2,8192,SCMP_CMP_MASKED_EQ,61440]
mknodat notify [2,24576,SCMP_CMP_MASKED_EQ,61440]`

const COMPAT_BLOCKING_POLICY = `[%s]
compat_sys_rt_sigaction errno 38
stub_x32_rt_sigreturn errno 38
compat_sys_ioctl errno 38
compat_sys_readv errno 38
compat_sys_writev errno 38
compat_sys_recvfrom errno 38
compat_sys_sendmsg errno 38
compat_sys_recvmsg errno 38
stub_x32_execve errno 38
compat_sys_ptrace errno 38
compat_sys_rt_sigpending errno 38
compat_sys_rt_sigtimedwait errno 38
compat_sys_rt_sigqueueinfo errno 38
compat_sys_sigaltstack errno 38
compat_sys_timer_create errno 38
compat_sys_mq_notify errno 38
compat_sys_kexec_load errno 38
compat_sys_waitid errno 38
compat_sys_set_robust_list errno 38
compat_sys_get_robust_list errno 38
compat_sys_vmsplice errno 38
compat_sys_move_pages errno 38
compat_sys_preadv64 errno 38
compat_sys_pwritev64 errno 38
compat_sys_rt_tgsigqueueinfo errno 38
compat_sys_recvmmsg errno 38
compat_sys_sendmmsg errno 38
compat_sys_process_vm_readv errno 38
compat_sys_process_vm_writev errno 38
compat_sys_setsockopt errno 38
compat_sys_getsockopt errno 38
compat_sys_io_setup errno 38
compat_sys_io_submit errno 38
stub_x32_execveat errno 38
`

var seccompPath = shared.VarPath("security", "seccomp")

func SeccompProfilePath(c container) string {
	return path.Join(seccompPath, c.Name())
}

func ContainerNeedsSeccomp(c container) bool {
	config := c.ExpandedConfig()

	keys := []string{
		"raw.seccomp",
		"security.syscalls.whitelist",
		"security.syscalls.blacklist",
	}

	for _, k := range keys {
		_, hasKey := config[k]
		if hasKey {
			return true
		}
	}

	compat := config["security.syscalls.blacklist_compat"]
	if shared.IsTrue(compat) {
		return true
	}

	/* this are enabled by default, so if the keys aren't present, that
	 * means "true"
	 */
	default_, ok := config["security.syscalls.blacklist_default"]
	if !ok || shared.IsTrue(default_) {
		return true
	}

	return false
}

func getSeccompProfileContent(c container) (string, error) {
	config := c.ExpandedConfig()

	raw := config["raw.seccomp"]
	if raw != "" {
		return raw, nil
	}

	policy := SECCOMP_HEADER

	whitelist := config["security.syscalls.whitelist"]
	if whitelist != "" {
		policy += "whitelist\n[all]\n"
		policy += whitelist
		return policy, nil
	}

	policy += "blacklist\n"

	default_, ok := config["security.syscalls.blacklist_default"]
	if !ok || shared.IsTrue(default_) {
		policy += DEFAULT_SECCOMP_POLICY
	}

	if !c.IsPrivileged() && !c.DaemonState().OS.RunningInUserNS && lxcSupportSeccompNotify(c.DaemonState()) {
		policy += SECCOMP_NOTIFY_POLICY
	}

	compat := config["security.syscalls.blacklist_compat"]
	if shared.IsTrue(compat) {
		arch, err := osarch.ArchitectureName(c.Architecture())
		if err != nil {
			return "", err
		}
		policy += fmt.Sprintf(COMPAT_BLOCKING_POLICY, arch)
	}

	blacklist := config["security.syscalls.blacklist"]
	if blacklist != "" {
		policy += blacklist
	}

	return policy, nil
}

func SeccompCreateProfile(c container) error {
	/* Unlike apparmor, there is no way to "cache" profiles, and profiles
	 * are automatically unloaded when a task dies. Thus, we don't need to
	 * unload them when a container stops, and we don't have to worry about
	 * the mtime on the file for any compiler purpose, so let's just write
	 * out the profile.
	 */
	if !ContainerNeedsSeccomp(c) {
		return nil
	}

	profile, err := getSeccompProfileContent(c)
	if err != nil {
		return nil
	}

	if err := os.MkdirAll(seccompPath, 0700); err != nil {
		return err
	}

	return ioutil.WriteFile(SeccompProfilePath(c), []byte(profile), 0600)
}

func SeccompDeleteProfile(c container) {
	/* similar to AppArmor, if we've never started this container, the
	 * delete can fail and that's ok.
	 */
	os.Remove(SeccompProfilePath(c))
}

type SeccompServer struct {
	d    *Daemon
	path string
	l    net.Listener
}

type SeccompIovec struct {
	ucred  *ucred
	memFd  int
	procFd int
	msg    *C.struct_seccomp_notify_proxy_msg
	req    *C.struct_seccomp_notif
	resp   *C.struct_seccomp_notif_resp
	cookie *C.char
	iov    *C.struct_iovec
}

func NewSeccompIovec(ucred *ucred) *SeccompIovec {
	msg_ptr := C.malloc(C.sizeof_struct_seccomp_notify_proxy_msg)
	msg := (*C.struct_seccomp_notify_proxy_msg)(msg_ptr)
	C.memset(msg_ptr, 0, C.sizeof_struct_seccomp_notify_proxy_msg)

	req_ptr := C.malloc(C.sizeof_struct_seccomp_notif)
	req := (*C.struct_seccomp_notif)(req_ptr)
	C.memset(req_ptr, 0, C.sizeof_struct_seccomp_notif)

	resp_ptr := C.malloc(C.sizeof_struct_seccomp_notif_resp)
	resp := (*C.struct_seccomp_notif_resp)(resp_ptr)
	C.memset(resp_ptr, 0, C.sizeof_struct_seccomp_notif_resp)

	cookie_ptr := C.malloc(64 * C.sizeof_char)
	cookie := (*C.char)(cookie_ptr)
	C.memset(cookie_ptr, 0, 64*C.sizeof_char)

	iov_unsafe_ptr := C.malloc(4 * C.sizeof_struct_iovec)
	iov := (*C.struct_iovec)(iov_unsafe_ptr)
	C.memset(iov_unsafe_ptr, 0, 4*C.sizeof_struct_iovec)

	C.prepare_seccomp_iovec(iov, msg, req, resp, cookie)

	return &SeccompIovec{
		memFd:  -1,
		procFd: -1,
		msg:    msg,
		req:    req,
		resp:   resp,
		cookie: cookie,
		iov:    iov,
		ucred:  ucred,
	}
}

func (siov *SeccompIovec) PutSeccompIovec() {
	if siov.memFd >= 0 {
		unix.Close(siov.memFd)
	}
	if siov.procFd >= 0 {
		unix.Close(siov.procFd)
	}
	C.free(unsafe.Pointer(siov.msg))
	C.free(unsafe.Pointer(siov.req))
	C.free(unsafe.Pointer(siov.resp))
	C.free(unsafe.Pointer(siov.cookie))
	C.free(unsafe.Pointer(siov.iov))
}

func (siov *SeccompIovec) ReceiveSeccompIovec(fd int) (uint64, error) {
	bytes, fds, err := netutils.AbstractUnixReceiveFdData(fd, 2, unsafe.Pointer(siov.iov), 4)
	if err != nil || err == io.EOF {
		return 0, err
	}

	if len(fds) == 2 {
		siov.procFd = int(fds[0])
		siov.memFd = int(fds[1])
	} else {
		siov.memFd = int(fds[0])
	}

	return bytes, nil
}

func (siov *SeccompIovec) IsValidSeccompIovec(size uint64) bool {
	if size < uint64(C.SECCOMP_MSG_SIZE_MIN) {
		logger.Warnf("Disconnected from seccomp socket after incomplete receive")
		return false
	}
	if siov.msg.__reserved != 0 {
		logger.Warnf("Disconnected from seccomp socket after client sent non-zero reserved field: pid=%v",
			siov.ucred.pid)
		return false
	}

	if siov.msg.sizes.seccomp_notif != C.expected_sizes.seccomp_notif {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_notif sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_notif, C.expected_sizes.seccomp_notif, siov.ucred.pid)
		return false
	}

	if siov.msg.sizes.seccomp_notif_resp != C.expected_sizes.seccomp_notif_resp {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_notif_resp sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_notif_resp, C.expected_sizes.seccomp_notif_resp, siov.ucred.pid)
		return false
	}

	if siov.msg.sizes.seccomp_data != C.expected_sizes.seccomp_data {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_data sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_data, C.expected_sizes.seccomp_data, siov.ucred.pid)
		return false
	}

	return true
}

func (siov *SeccompIovec) SendSeccompIovec(fd int, goErrno int) error {
	C.seccomp_notify_mknod_update_response(siov.resp, C.int(goErrno))

	msghdr := C.struct_msghdr{}
	msghdr.msg_iov = siov.iov
	msghdr.msg_iovlen = 4 - 1 // without cookie
	bytes := C.sendmsg(C.int(fd), &msghdr, C.MSG_NOSIGNAL)
	if bytes < 0 {
		logger.Debugf("Disconnected from seccomp socket after failed write: pid=%v", siov.ucred.pid)
		return fmt.Errorf("Failed to send response to seccomp client %v", siov.ucred.pid)
	}

	if uint64(bytes) != uint64(C.SECCOMP_MSG_SIZE_MIN) {
		logger.Debugf("Disconnected from seccomp socket after short write: pid=%v", siov.ucred.pid)
		return fmt.Errorf("Failed to send full response to seccomp client %v", siov.ucred.pid)
	}

	return nil
}

func NewSeccompServer(d *Daemon, path string) (*SeccompServer, error) {
	ret := C.seccomp_notify_get_sizes(&C.expected_sizes)
	if ret < 0 {
		return nil, fmt.Errorf("Failed to query kernel for seccomp notifier sizes")
	}

	// Cleanup existing sockets
	if shared.PathExists(path) {
		err := os.Remove(path)
		if err != nil {
			return nil, err
		}
	}

	// Bind new socket
	l, err := net.Listen("unixpacket", path)
	if err != nil {
		return nil, err
	}

	// Restrict access
	err = os.Chmod(path, 0700)
	if err != nil {
		return nil, err
	}

	// Start the server
	s := SeccompServer{
		d:    d,
		path: path,
		l:    l,
	}

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}

			go func() {
				ucred, err := getCred(c.(*net.UnixConn))
				if err != nil {
					logger.Errorf("Unable to get ucred from seccomp socket client: %v", err)
					return
				}

				logger.Debugf("Connected to seccomp socket: pid=%v", ucred.pid)

				unixFile, err := c.(*net.UnixConn).File()
				if err != nil {
					return
				}

				for {
					siov := NewSeccompIovec(ucred)
					bytes, err := siov.ReceiveSeccompIovec(int(unixFile.Fd()))
					if err != nil {
						logger.Debugf("Disconnected from seccomp socket after failed receive: pid=%v, err=%s", ucred.pid, err)
						c.Close()
						return
					}

					if siov.IsValidSeccompIovec(bytes) {
						go s.Handler(c, int(unixFile.Fd()), siov)
					} else {
						go s.InvalidHandler(c, int(unixFile.Fd()), siov)
					}
				}
			}()
		}
	}()

	return &s, nil
}

func taskUidGid(pid int) (error, int32, int32) {
	status, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return err, -1, -1
	}

	reUid := regexp.MustCompile("Uid:\\s*([0-9]*)\\s*([0-9]*)")
	reGid := regexp.MustCompile("Gid:\\s*([0-9]*)\\s*([0-9]*)")
	var gid int32
	var uid int32
	uidFound := false
	gidFound := false
	for _, line := range strings.Split(string(status), "\n") {
		if uidFound && gidFound {
			break
		}

		if !uidFound {
			m := reUid.FindStringSubmatch(line)
			if m != nil && len(m) > 2 {
				// effective uid
				result, err := strconv.Atoi(m[2])
				if err != nil {
					return err, -1, -1
				}

				uid = int32(result)
				uidFound = true
				continue
			}
		}

		if !gidFound {
			m := reGid.FindStringSubmatch(line)
			if m != nil && len(m) > 2 {
				// effective gid
				result, err := strconv.Atoi(m[2])
				if err != nil {
					return err, -1, -1
				}

				gid = int32(result)
				gidFound = true
				continue
			}
		}
	}

	return nil, uid, gid
}

func (s *SeccompServer) doMknod(c container, dev types.Device, requestPID int, permissionsOnly bool) int {
	goErrno := int(-C.EPERM)

	rootLink := fmt.Sprintf("/proc/%d/root", requestPID)
	rootPath, err := os.Readlink(rootLink)
	if err != nil {
		return goErrno
	}

	err, uid, gid := taskUidGid(requestPID)
	if err != nil {
		return goErrno
	}

	deBool := func() int {
		if permissionsOnly {
			return 1
		}

		return 0
	}

	if !path.IsAbs(dev["path"]) {
		cwdLink := fmt.Sprintf("/proc/%d/cwd", requestPID)
		prefixPath, err := os.Readlink(cwdLink)
		if err != nil {
			return goErrno
		}

		prefixPath = strings.TrimPrefix(prefixPath, rootPath)
		dev["hostpath"] = filepath.Join(rootPath, prefixPath, dev["path"])
	} else {
		dev["hostpath"] = filepath.Join(rootPath, dev["path"])
	}

	_, stderr, err := shared.RunCommandSplit(util.GetExecPath(),
		"forkmknod", dev["pid"], dev["path"],
		dev["mode_t"], dev["dev_t"], dev["hostpath"],
		fmt.Sprintf("%d", uid), fmt.Sprintf("%d", gid),
		fmt.Sprintf("%d", deBool()))
	if err != nil {
		tmp, err2 := strconv.Atoi(stderr)
		if err2 == nil && tmp != C.ENOANO {
			goErrno = -tmp
		}

		return goErrno
	}

	return 0
}

// InvalidHandler sends a dummy message to LXC. LXC will notice the short write
// and send a default message to the kernel thereby avoiding a 30s hang.
func (s *SeccompServer) InvalidHandler(c net.Conn, clientFd int, siov *SeccompIovec) {
	msghdr := C.struct_msghdr{}
	C.sendmsg(C.int(clientFd), &msghdr, C.MSG_NOSIGNAL)
	siov.PutSeccompIovec()
}

func (s *SeccompServer) Handler(c net.Conn, clientFd int, siov *SeccompIovec) error {
	logger.Debugf("Handling seccomp notification from: %v", siov.ucred.pid)

	defer siov.PutSeccompIovec()

	var cMode C.mode_t
	var cDev C.dev_t
	var cPid C.pid_t
	var err error
	goErrno := 0
	cPathBuf := [unix.PathMax]C.char{}
	goErrno = int(C.seccomp_notify_mknod_set_response(C.int(siov.memFd), siov.req, siov.resp,
		&cPathBuf[0],
		unix.PathMax, &cMode,
		&cDev, &cPid))
	if goErrno == 0 {
		dev := types.Device{}
		dev["type"] = "unix-char"
		dev["mode"] = fmt.Sprintf("%#o", cMode)
		dev["major"] = fmt.Sprintf("%d", unix.Major(uint64(cDev)))
		dev["minor"] = fmt.Sprintf("%d", unix.Minor(uint64(cDev)))
		dev["pid"] = fmt.Sprintf("%d", cPid)
		dev["path"] = C.GoString(&cPathBuf[0])
		dev["mode_t"] = fmt.Sprintf("%d", cMode)
		dev["dev_t"] = fmt.Sprintf("%d", cDev)

		c, _ := findContainerForPid(int32(siov.msg.monitor_pid), s.d)
		if c != nil {
			diskIdmap, err2 := c.DiskIdmap()
			if err2 != nil {
				return siov.SendSeccompIovec(clientFd, int(-C.EPERM))
			}

			if s.d.os.Shiftfs && !c.IsPrivileged() && diskIdmap == nil {
				goErrno = s.doMknod(c, dev, int(cPid), true)
				if goErrno == int(-C.ENOMEDIUM) {
					err = c.InsertSeccompUnixDevice(fmt.Sprintf("forkmknod.unix.%d", int(cPid)), dev, int(cPid))
					if err != nil {
						goErrno = int(-C.EPERM)
					} else {
						goErrno = 0
					}
				}
			} else {
				goErrno = s.doMknod(c, dev, int(cPid), false)
				if goErrno == int(-C.ENOMEDIUM) {
					err = c.InsertSeccompUnixDevice(fmt.Sprintf("forkmknod.unix.%d", int(cPid)), dev, int(cPid))
					if err != nil {
						goErrno = int(-C.EPERM)
					} else {
						goErrno = 0
					}
				}
			}
		}

		if goErrno != 0 {
			logger.Errorf("Failed to inject device node into container %s (errno = %d)", c.Name(), -goErrno)
		}
	}

	err = siov.SendSeccompIovec(clientFd, goErrno)
	if err != nil {
		return err
	}

	logger.Debugf("Handled seccomp notification from: %v", siov.ucred.pid)
	return nil
}

func (s *SeccompServer) Stop() error {
	os.Remove(s.path)
	return s.l.Close()
}
