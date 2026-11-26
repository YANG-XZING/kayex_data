// Package cleaner 负责定时清理 SOP 文件目录，只保留最近一段时间内的文件。
// SOP 文件由 consumers.HandleSOP 写入 config.GetSOPRoot()/<IP>/<文件名_时间>.csv，
// 其中每个 <IP> 子目录对应一个炉台。
package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"hmi1.1_consumer/config"
	"hmi1.1_consumer/logx"
)

var (
	// retention 保留时长：仅保留最近一个月(30 天)内的 SOP 文件，更早的删除。
	retention = config.SOPRetention
	// scanInterval 清理任务的执行间隔：每 24 小时扫描一次。
	scanInterval = config.SOPScanInterval
)

// Start 启动 SOP 文件定时清理协程：启动时先清一次，之后每 scanInterval 清一次。
// 通过 ctx 支持随主服务一起优雅退出。
func Start(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	logx.Infof("CLEAN", "SOP 定时清理已启动：保留最近 %d 天，每个炉台至少保留 1 个文件，每 %s 执行一次",
		int(retention.Hours()/24), scanInterval)

	// 启动即执行一次，避免等满一个周期
	cleanOnce()

	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logx.Infof("CLEAN", "收到退出信号，SOP 清理协程终止")
			return
		case <-ticker.C:
			cleanOnce()
		}
	}
}

// cleanOnce 遍历 SOP 根目录下的每个炉台(IP)子目录，逐个清理，并汇总本轮结果。
func cleanOnce() {
	root := config.GetSOPRoot()

	info, err := os.Stat(root)
	if err != nil {
		// 目录还不存在(尚未产生任何 SOP)或暂不可访问：跳过本轮，不视为错误。
		logx.Warnf("CLEAN", "SOP 根目录不可访问，跳过本轮清理: %v", err)
		return
	}
	if !info.IsDir() {
		logx.Errorf("CLEAN", "SOP 根路径不是目录，已跳过: %s", root)
		return
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		logx.Warnf("CLEAN", "读取 SOP 根目录失败，跳过本轮清理: %v", err)
		return
	}

	cutoff := time.Now().Add(-retention)
	var deleted, failed, protected, removedDirs int

	for _, e := range entries {
		if !e.IsDir() {
			continue // 只处理炉台(IP)子目录，忽略根目录下的散落文件
		}
		dir := filepath.Join(root, e.Name())

		d, f, p, empty := cleanFurnaceDir(dir, cutoff)
		deleted += d
		failed += f
		protected += p

		// 本就没有任何文件的空目录(理论上保底规则不会产生)：顺手删掉
		if empty {
			if os.Remove(dir) == nil {
				removedDirs++
			}
		}
	}

	// 每轮都打一条 INFO 回执，便于确认清理确实执行过（即使本轮无过期文件）
	logx.Infof("CLEAN", "SOP 清理完成：删除文件 %d 个，保底保留 %d 个，删除空目录 %d 个，失败 %d 个",
		deleted, protected, removedDirs, failed)
}

// cleanFurnaceDir 清理单个炉台(IP)目录：删除修改时间早于 cutoff 的文件，
// 但【始终保留该目录内最新的一个文件】，保证每个炉台至少留有一个 SOP。
// 返回：删除数、失败数、保底保留数(最新文件本身也超期却被强制保留时计 1)、目录是否本就为空。
func cleanFurnaceDir(dir string, cutoff time.Time) (deleted, failed, protected int, empty bool) {
	items, err := os.ReadDir(dir)
	if err != nil {
		logx.Warnf("CLEAN", "读取炉台目录失败 %s: %v", dir, err)
		return 0, 0, 0, false // 读失败不当作空目录，避免误删
	}

	// 收集常规文件及其修改时间
	type fileMeta struct {
		path    string
		modTime time.Time
	}
	var files []fileMeta
	for _, it := range items {
		if it.IsDir() {
			continue // 该层不应有子目录，保险起见跳过
		}
		fi, err := it.Info()
		if err != nil {
			continue
		}
		files = append(files, fileMeta{filepath.Join(dir, it.Name()), fi.ModTime()})
	}

	if len(files) == 0 {
		return 0, 0, 0, true // 本就是空目录
	}

	// 找出最新文件的下标：它无论多旧都要保留(每个炉台至少留一个)
	newest := 0
	for i := 1; i < len(files); i++ {
		if files[i].modTime.After(files[newest].modTime) {
			newest = i
		}
	}

	for i, f := range files {
		if i == newest {
			continue // 始终保留最新的一个，绝不删
		}
		if f.modTime.Before(cutoff) {
			if err := os.Remove(f.path); err != nil {
				failed++
				logx.Warnf("CLEAN", "删除过期文件失败 %s: %v", f.path, err)
			} else {
				deleted++
			}
		}
	}

	// 最新文件本身也超期、但因保底规则被留下：记一笔，方便你在日志里看到"有炉台的最新SOP也已很旧"
	if files[newest].modTime.Before(cutoff) {
		protected = 1
	}

	// 因始终保留最新文件，处理后目录必不为空
	return deleted, failed, protected, false
}
