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
	return startProcessWithDiagnostics(ctx, cmd, nil)
}

func startProcessWithDiagnostics(ctx context.Context, cmd *exec.Cmd, trace *helperTrace) (func(), func(), error) {
	started := trace.begin()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		trace.recordError(helperJobSetup, started, err)
		return nil, nil, err
	}
	release := func() {
		started := trace.begin()
		err := windows.CloseHandle(job)
		trace.recordError(helperRelease, started, err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	info.BasicLimitInformation.ActiveProcessLimit = 32
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		trace.recordError(helperJobSetup, started, err)
		release()
		return nil, nil, err
	}
	trace.recordError(helperJobSetup, started, nil)
	if err = ctx.Err(); err != nil {
		release()
		return nil, nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED, HideWindow: true}
	started = trace.begin()
	err = cmd.Start()
	trace.recordError(helperStart, started, err)
	if err != nil {
		release()
		return nil, nil, err
	}
	abort := func(e error) (func(), func(), error) {
		started := trace.begin()
		killErr := cmd.Process.Kill()
		waitErr := cmd.Wait()
		// This join belongs to failed-start reclamation, not the normal wait
		// stage. It must not replace the failure that required reclamation.
		if killErr != nil {
			trace.recordError(helperReclaim, started, killErr)
		} else {
			trace.recordError(helperReclaim, started, waitErr)
		}
		release()
		return nil, nil, e
	}
	started = trace.begin()
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		trace.recordError(helperAssign, started, err)
		return abort(err)
	}
	err = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	trace.recordError(helperAssign, started, err)
	if err != nil {
		return abort(err)
	}
	if err = ctx.Err(); err != nil {
		return abort(err)
	}
	started = trace.begin()
	err = resumeInitialThread(uint32(cmd.Process.Pid))
	trace.recordError(helperResume, started, err)
	if err != nil {
		return abort(err)
	}
	return func() {
		started := trace.begin()
		err := windows.TerminateJobObject(job, 1)
		trace.recordError(helperReclaim, started, err)
	}, release, nil
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
