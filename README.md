# TSP Manager (`tspm`)

一个面向 Linux 的 Task Spooler 多队列管理器。它不替代 Task Spooler，也不实现自己的任务调度器；所有任务操作都通过原生 `tsp` CLI 完成。

## 特性

- 一个 TUI 同时查看多个 Task Spooler 队列。
- 自动发现 Linux `/proc/net/unix` 中名称符合常见 tsp 规则的活跃 Unix socket。
- 支持手动登记任意命名的 `TS_SOCKET`，这是自动发现的可靠兜底。
- 查看队列运行数、等待数、完成数和并发 slots。
- 查看任务列表、任务信息和任务输出。
- 提交命令、发送 SIGTERM、移除任务、清理完成任务、提升任务优先级、修改并发数。
- CLI 和 JSON 状态输出，方便 Shell 脚本或 Agent 使用。
- 纯 Go 标准库实现，无第三方 Go module 依赖；Linux amd64 可编译为单文件二进制。
- 不需要 root，不需要常驻 daemon。

## 系统要求

- Linux（首个发布版本的交互式 TUI 仅支持 Linux）。
- 已安装 Task Spooler，并且 `tsp` 在 `PATH` 中。
- 建议使用支持 ANSI escape sequences 的终端。

> `tspm` 不会安装或启动 Task Spooler。它只连接已经存在的 tsp server/socket，避免扫描未知 socket 时意外创建队列。

## 安装

### 方式一：使用预编译二进制

将 `tspm` 放到用户自己的 PATH 目录即可，不需要 sudo：

```bash
mkdir -p ~/.local/bin
install -m 755 ./tspm ~/.local/bin/tspm

# 如果 ~/.local/bin 尚未加入 PATH：
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc

tspm --version
```

### 方式二：从源码编译

需要 Go 1.23 或更新版本：

```bash
git clone <your-repository-url> tsp-manager
cd tsp-manager
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o tspm ./cmd/tspm
install -m 755 ./tspm ~/.local/bin/tspm
```

如果使用本项目提供的二进制发行包，也可以解压后运行：

```bash
./install.sh
```

## 快速开始

先用原生 `tsp` 创建一个队列：

```bash
tsp sleep 60
```

再启动管理器：

```bash
tspm
```

如果已经通过 `TS_SOCKET` 创建多个队列，`tspm` 会尝试自动发现常见命名的 socket，例如：

- 默认 socket：`$TMPDIR/socket-ts.<uid>`
- 自定义 socket：路径中包含 `tsp-`、`tsp_`、`ts-`、`ts_`、`socket-ts` 或 `task-spooler` 的活跃 Unix socket（`ts-out.*` 瞬时输出 socket 除外）

任意命名的 socket 不保证能被安全地自动识别。请显式登记：

```bash
tspm queue add gpu0 /tmp/my-gpu0-queue.sock
tspm queue add gpu1 /tmp/my-gpu1-queue.sock
```

配置保存在：

```text
~/.config/tspm/config.json
```

也可以查看配置路径或生成默认配置：

```bash
tspm config path
tspm config init
```

示例配置见 `examples/config.json`。

## TUI 快捷键

| 按键 | 操作 |
|---|---|
| `↑` / `↓` | 在当前窗格移动选择 |
| `Tab` / `←` / `→` | 在队列窗格和任务窗格之间切换 |
| `Enter` | 查看选中任务详情 |
| `a` | 向当前队列提交 Shell 命令 |
| `n` | 登记一个队列（输入名称和 socket 路径） |
| `d` | 移除当前队列的配置登记，不会停止 tsp server |
| `k` | 对选中任务发送 SIGTERM（tsp -k） |
| `x` | 从队列移除选中任务（tsp -r） |
| `C` | 清理当前队列的已完成任务（tsp -C） |
| `u` | 将选中任务提前（tsp -u） |
| `s` | 修改当前队列最大并发数（tsp -S） |
| `l` | 查看任务输出文件的尾部，并定期刷新 |
| `r` | 立即刷新 |
| `?` | 快捷键帮助 |
| `q` / `Esc` / `Ctrl-C` | 退出 |

日志视图中，`↑` 查看更早的日志，`↓` 回到较新的日志，`Esc` 退出日志视图。

## CLI

```bash
# 列出队列
tspm queues

# 查看所有队列和任务的 JSON 状态
tspm status

# 检查 tsp 二进制和 socket 状态
tspm doctor

# 登记 / 取消登记队列
tspm queue add gpu0 /tmp/tsp-gpu0.sock
tspm queue remove gpu0

# 向指定队列提交任务
tspm submit -q gpu0 -- python train.py --epochs 3

# 使用其他配置文件或 tsp 二进制
tspm --config /path/to/config.json queues
tspm --tsp-bin /opt/bin/tsp doctor
```

注意：`tspm submit` 的多个参数会分别进行 shell-quote 后作为命令参数执行。如果需要管道、重定向或复合命令，请把整条 shell 命令作为一个参数传入，例如 `tspm submit -q gpu0 -- 'python train.py | tee train.log'`。TUI 的 `a` 操作直接把输入作为 `sh -c` 命令执行。请只提交你信任的命令。

## 自动发现机制与限制

1. 配置文件登记的 socket 始终会显示，即使它当前离线。
2. Linux 自动发现读取 `/proc/net/unix`，只考虑处于监听状态（`St=01`）且路径名符合常见 tsp 命名规则的 Unix socket。
3. `TS_SOCKET` 环境变量和默认 `$TMPDIR/socket-ts.<uid>` 路径也会作为候选。
4. 不会对任意未知 socket 执行 `tsp -l`。Task Spooler 客户端在某些情况下可能尝试启动 server，因此盲目探测任意 socket 有副作用风险。
5. 对路径完全自定义、名称不包含 tsp 标记的 socket，请运行 `tspm queue add <name> <socket>`。

## 设计说明

- `discovery`：发现活跃 Unix socket；
- `spooler`：通过 `TS_SOCKET=<path> tsp ...` 调用原生 CLI；
- `manager`：并行刷新多个队列，并提供统一操作接口；
- `ui`：纯 ANSI TUI，后台刷新与键盘交互分离；
- `config`：JSON 配置，原子写入。

任务日志使用 `tsp -o <id>` 获取输出文件路径，再由程序读取文件尾部。不会在 UI 主线程中运行会持续阻塞的 `tsp -t <id>`。

## 当前版本范围

- v0.1.0 目标为 Linux amd64。
- Linux arm64 可以从源码编译；Release workflow 也配置了 amd64/arm64 构建。
- 非 Linux 平台当前仅保留 CLI 代码的编译兼容性，交互式 TUI 暂不支持。
- tsp 不同 fork 的 `-l` 输出格式可能有差异。解析器兼容常见 Task Spooler 1.0.x 表格格式；遇到不兼容格式时请提交 `tsp -l` 输出样例（注意先移除敏感命令参数）。
- 不提供 `tsp -K` 按钮，避免误停止整个队列 server。

## 开发与测试

```bash
gofmt -w .
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o dist/tspm-linux-amd64 ./cmd/tspm
```

## License

MIT，见 `LICENSE`。
