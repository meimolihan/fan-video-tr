package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/meimolihan/fan-video-tr/internal/version"
)

// run 解析命令行并分发到对应命令。
func run() error {
	args := os.Args[1:]

	// 兼容 flag 形式：-version / -v / -h / --version / --help
	for _, a := range args {
		switch a {
		case "-version", "-v", "--version":
			printVersion()
			return nil
		case "-h", "--help":
			printHelp()
			return nil
		}
	}

	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		// 首参为非选项：按子命令分发，未知命令报错
		sub, rest := args[0], args[1:]
		switch sub {
		case "status":
			return runStatus()
		case "uninstall":
			return runUninstall(rest)
		case "start", "stop", "restart":
			return runService(sub)
		case "backup":
			fmt.Fprintln(os.Stderr, "提示: 备份请直接归档数据目录（app.data_dir）。")
			return nil
		case "restore":
			fmt.Fprintln(os.Stderr, "提示: 恢复请将备份解压回数据目录后重启服务。")
			return nil
		case "help":
			printHelp()
			return nil
		case "version":
			printVersion()
			return nil
		default:
			errPrint("未知命令:", sub)
			fmt.Println()
			commandUsage()
			return fmt.Errorf("未知命令: %s", sub)
		}
	}

	// 其余情况：服务启动选项（--port / --data / --media / --output / --worker 等）
	fs := flag.NewFlagSet("fan-video-tr", flag.ExitOnError)
	dataDir := fs.String("data", "", "数据目录（默认 ./data 或配置 app.data_dir）")
	port := fs.Int("port", 0, "监听端口（默认 8790 或配置 app.port）")
	mediaDir := fs.String("media", "", "视频浏览根目录（默认全盘或配置 app.media_dir）")
	outputDir := fs.String("output", "", "转码输出目录（默认 <data>/output 或配置 app.output_dir）")
	worker := fs.Int("worker", 0, "转码并发数（默认 1 或配置 app.worker）")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 允许 -port 0 表示使用配置默认值
	var portPtr *int
	if *port > 0 {
		portPtr = port
	}
	var workerPtr *int
	if *worker > 0 {
		workerPtr = worker
	}

	return runServer(*dataDir, portPtr, *mediaDir, *outputDir, workerPtr)
}

// parseCommand 从参数中识别首个子命令并返回剩余参数；无法识别时返回 ""。
// printVersion 输出版本信息。
func printVersion() {
	fmt.Printf("fan-video-tr v%s\n", version.Current())
}

// printHelp 打印帮助（banner + 命令表 + 访问地址 + 示例）。
func printHelp() {
	banner("管理命令")
	rows := [][2]string{
		{"status", "显示运行方式（systemd / 直接运行）、PID、端口、访问地址、运行时长、内存、路径、健康检查"},
		{"start | stop | restart", "启动 / 停止 / 重启服务（systemd 优先，其次直接管理进程）"},
		{"uninstall [-y] [--purge|--keep-data]", "停止并移除服务/进程，删除程序与安装记录；可选删除数据目录"},
		{"backup | restore", "备份 / 恢复数据目录操作说明"},
		{"version, -version, --version, -v", "显示版本号"},
		{"help, -h, --help", "显示本帮助"},
		{"（无命令，加 -port/-data/-media/-output/-worker）", "启动 Web 服务"},
	}
	for _, r := range rows {
		padded := r[0] + strings.Repeat(" ", 40-len(r[0]))
		fmt.Printf("  %s %s\n", paint(padded, clrWhite), paint(r[1], clrGrey))
	}
	fmt.Println()
	sectionPrint("访问地址")
	printAccess(defaultPortFromRecord())
	fmt.Println()
	fmt.Println(paint("使用示例：", clrCyan))
	for _, line := range []string{
		"  fan-video-tr status",
		"  fan-video-tr restart",
		"  fan-video-tr -port 8790 -data /var/lib/fan-video-tr/data -media /vol2/1000/videos -worker 2",
		"  sudo fan-video-tr uninstall -y            # 免确认卸载，保留数据目录",
		"  sudo fan-video-tr uninstall -y --purge    # 免确认卸载，并删除数据目录",
	} {
		fmt.Println(paint("    "+line, clrGreen))
	}
	return
}

// commandUsage 打印简短用法（未知参数等场景）。
func commandUsage() {
	fmt.Println("用法: fan-video-tr [命令] [选项]")
	fmt.Println()
	fmt.Println("命令:")
	for _, r := range [][2]string{
		{"status", "查看运行状态"},
		{"start | stop | restart", "启动 / 停止 / 重启服务"},
		{"uninstall [-y] [--purge|--keep-data]", "停止服务并移除安装"},
		{"version", "显示版本号"},
		{"help", "显示帮助"},
		{"backup | restore", "备份 / 恢复说明"},
	} {
		fmt.Printf("  %-38s%s\n", paint(r[0], clrWhite), paint(r[1], clrGrey))
	}
	fmt.Println()
	fmt.Println("（无命令时启动 Web 服务，可加 -port/-data/-media/-output/-worker 选项）")
}

// confirm 交互式二次确认。
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	var input string
	_, err := fmt.Scanln(&input)
	if err != nil {
		return false
	}
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}
