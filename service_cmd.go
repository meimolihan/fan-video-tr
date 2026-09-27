package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runService 启动 / 停止 / 重启管理服务。
func runService(action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		errPrint("未知操作:", action)
		return fmt.Errorf("未知操作: %s", action)
	}

	actionName := map[string]string{"start": "启动", "stop": "停止", "restart": "重启"}[action]
	banner(actionName + "服务")
	sepPrint()

	if isSystemd() {
		sectionPrint("systemd 服务")
		if err := exec.Command("systemctl", action, "fan-video-tr.service").Run(); err != nil {
			errPrint(fmt.Sprintf("systemctl %s fan-video-tr 执行失败: %v", action, err))
			sepPrint()
			return err
		}
		donePrint("systemctl", action, "fan-video-tr.service 完成")
		sepPrint()
		if action != "stop" {
			rec := readRecord()
			port := defaultPort()
			if v, err := strconvAtoiSafe(rec["PORT"]); err == nil && v > 0 {
				port = v
			}
			sectionPrint("访问地址")
			printAccess(port)
			fmt.Println()
		}
		return nil
	}

	// 非 systemd：直接管理进程
	sectionPrint("直接运行进程")
	if action == "stop" {
		stopByPidFile()
		// 通过 /proc 匹配补充终止
		rec := readRecord()
		binPath := floatNull(rec["BIN_PATH"], defaultBinPath)
		for _, pid := range matchByProc(binPath) {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
		}
		_ = os.Remove(defaultPidFile)
		donePrint("已停止全部 fan-video-tr 进程")
		sepPrint()
		return nil
	}

	if action == "start" || action == "restart" {
		if action == "restart" {
			stopByPidFile()
			for _, pid := range matchByProc(defaultBinPath) {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
			_ = os.Remove(defaultPidFile)
		}
		rec := readRecord()
		dataDir := floatNull(rec["DATA_DIR"], defaultDataDir)
		binPath := floatNull(rec["BIN_PATH"], defaultBinPath)
		_ = os.MkdirAll(dataDir, 0755)
		logPath := filepath.Join(dataDir, "fan-video-tr.log")
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			errPrint(fmt.Sprintf("打开日志文件失败: %v", err))
			sepPrint()
			return err
		}
		defer logFile.Close()

		cmd := exec.Command(binPath, "--port", fmt.Sprintf("%d", defaultPortFromRecord()), "--data", dataDir, "--media", mediaDirFromRecord())
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			errPrint(fmt.Sprintf("启动服务失败: %v", err))
			sepPrint()
			return err
		}
		_ = os.WriteFile(defaultPidFile, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0644)
		donePrint(fmt.Sprintf("已在后台启动，pid: %d", cmd.Process.Pid))
		sepPrint()
		rec = readRecord()
		port := defaultPort()
		if v, err := strconvAtoiSafe(rec["PORT"]); err == nil && v > 0 {
			port = v
		}
		sectionPrint("访问地址")
		printAccess(port)
		fmt.Println()
	}
	return nil
}

func strconvAtoiSafe(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func defaultPortFromRecord() int {
	rec := readRecord()
	if v, err := strconvAtoiSafe(rec["PORT"]); err == nil && v > 0 {
		return v
	}
	return defaultPort()
}

func mediaDirFromRecord() string {
	rec := readRecord()
	v := floatNull(rec["VIDEO_DIR"], "")
	return strings.TrimSpace(v)
}
