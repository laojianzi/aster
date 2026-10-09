package credentialexec

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Start suspended, assign to a kill-on-close Job Object, then resume the sole
// initial thread. No plugin instruction can run before the job is assigned.
func startProcess(ctx context.Context, cmd *exec.Cmd) (func(), func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	info.BasicLimitInformation.ActiveProcessLimit = 32
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, nil, err
	}
	if err = ctx.Err(); err != nil {
		windows.CloseHandle(job)
		return nil, nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED, HideWindow: true}
	if err = cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, nil, err
	}
	abort := func(e error) (func(), func(), error) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return nil, nil, e
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return abort(err)
	}
	err = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if err != nil {
		return abort(err)
	}
	if err = ctx.Err(); err != nil {
		return abort(err)
	}
	if err = resumeInitialThread(uint32(cmd.Process.Pid)); err != nil {
		return abort(err)
	}
	return func() { _ = windows.TerminateJobObject(job, 1) }, func() { _ = windows.CloseHandle(job) }, nil
}
func resumeInitialThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, e := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if e != nil {
			return e
		}
		_, e = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return e
	}
	return errors.New("suspended authentication thread not found")
}
