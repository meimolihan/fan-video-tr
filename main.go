// fan-video-tr 视频转码工具：内置 FFmpeg 能力的 Web 服务。
//
// 用法：
//
//	fan-video-tr [命令] [选项]
//
// 命令：
//
//	status          查看运行状态、访问地址、资源占用
//	start|stop|restart  启动 / 停止 / 重启服务
//	uninstall [-y] [--purge|--keep-data]  停止服务并移除安装
//	version / -v    打印版本号
//	help / -h       显示帮助
//	backup|restore  备份 / 恢复说明
//	（无命令时启动 Web 服务，可加 -port/-data/-media/-output/-worker 选项）
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
