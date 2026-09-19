# Telegram 部署

本功能接收白名单用户私聊转发的视频和视频文件，保存到本地视频库，并可定时转存网盘。接收设置位于 **配置面板 → Telegram**，任务和转存设置位于 **Telegram** 页面。消息链接、群组监听和历史消息抓取暂不支持。

项目通过标准 HTTP 接口连接独立部署的 Local Bot API Server，不管理其进程。默认部署使用第三方预构建镜像 `aiogram/telegram-bot-api:latest`，其中运行 Telegram 官方服务端程序。项目不再构建或发布 Bot API 镜像。[镜像说明](https://hub.docker.com/r/aiogram/telegram-bot-api)

## 配置归属

| 配置 | 保存位置 | 生效方式 |
| --- | --- | --- |
| Bot Token、白名单、站点地址、任务限制 | 网站面板对应的 `config.yaml` | 保存后自动应用 |
| Bot API 地址 | 网站面板对应的 `config.yaml` | 保存后自动应用 |
| TG 数据目录 | Compose 文件中的共享数据挂载 | 重建 Bot API 并重启网站后读取 |
| 网盘转存设置 | 网站面板对应的 `config.yaml` | 下一次转存任务使用新配置 |
| API ID、API Hash | Bot API 部署环境；配套示例使用 `.env.telegram` | 重新创建 Bot API 容器 |
| Local 模式、监听端口、持久化卷 | Docker Compose 或其他部署工具 | 重新创建 Bot API 服务 |

Bot Token 从 BotFather 获取。API ID/API Hash 从 Telegram 开发者管理页面申请，供独立服务连接 Telegram 使用；它们不再通过网站面板保存或下发。[应用凭据说明](https://core.telegram.org/api/obtaining_api_id)

## Docker 部署

在项目根目录准备部署凭据文件：

```bash
cp .env.telegram.example .env.telegram
chmod 600 .env.telegram
```

编辑 `.env.telegram`，填写实际值：

```dotenv
TELEGRAM_API_ID=你的应用ID
TELEGRAM_API_HASH=你的应用Hash
```

然后启动：

```bash
docker compose --env-file .env.telegram -f docker-compose.yml -f docker-compose.telegram.yml up -d --pull always
```

Compose 使用 `TELEGRAM_LOCAL=1` 启用大文件和本地路径模式，使用 `TELEGRAM_HTTP_PORT=7878` 对齐项目默认地址。aiogram 镜像自身的默认端口是 8081；如果接入另外部署的服务，应填写该服务实际的地址和端口。

网站通过 `http://telegram-bot-api:7878` 连接配套服务，端口仅在 Docker 网络内使用。Bot API 的 API 凭据只注入 Bot API 容器；Bot Token 在网站面板填写。`.env.telegram` 已被 Git 和 Docker 构建上下文忽略。

网站容器只读挂载 `docker-compose.telegram.yml`，启动时从其中两个服务的挂载关系识别共享目录。两个容器都将 `telegram-cache` 卷读写挂载到 `/var/lib/telegram-bot-api`。其中包含 Bot API 数据和项目管理的 `library/` 正式视频目录，不能将整个卷当作临时缓存清空。

需要选择其他已发布版本时，在 `.env.telegram` 中设置 `TELEGRAM_BOT_API_IMAGE`，例如固定镜像摘要。升级 Bot API 时重新执行启动命令。镜像覆盖适用于接受这些环境变量并使用配套数据目录约定的部署。

## 后台设置

1. 打开 **配置面板 → Telegram**，填写 Bot Token。
2. 填写 Bot API 地址；全 Docker 配套部署可以使用默认值。文件目录直接从 Compose 读取，不在 Web 中展示或编辑。
3. 开启接入，填写允许的用户数字 ID。首次不知道 ID 时可以留空白名单。
4. 可选填写站点地址，用于成功通知中的视频详情链接。
5. 保存配置，进入 **Telegram** 查看连接状态。连接成功后显示机器人用户名。
6. 白名单为空时，私聊机器人发送 `/id`，将回复的数字 ID 填入允许用户列表并再次保存。
7. 转发一个较小的视频，确认后台记录变为“已保存”，并能打开视频详情；然后验证超过 20 MB 的视频。

关闭接入后隐藏 Telegram 页面入口，停止项目的接收、通知和正在进行的 TG 获取；待处理任务等待重新启用，已经入库的视频仍可访问。关闭接入不会停止独立 Bot API 服务。普通直链导入不受影响。

接收设置变化时，项目先取消并等待旧接收器退出，再建立新会话；活动下载会中断并等待重新连接。网盘转存设置变化不重建接收器。API ID/API Hash 变化时，需要更新 `.env.telegram` 并重新执行 Compose 启动命令。

连接检查通过 `getMe`、`getWebhookInfo` 和共享目录可读性判断，不要求项目专用配置文件或心跳。检查使用已保存的配置，不启动轮询、不消费消息、不发送 TG 消息。检查成功不代表目录写入和大文件下载已经验证，实际导入才是完整验证。

Bot API 暂时不可用时显示连接异常，项目自动等待并重试；服务恢复后继续接收。可通过下面的命令检查容器日志：

```bash
docker compose --env-file .env.telegram -f docker-compose.yml -f docker-compose.telegram.yml logs telegram-bot-api
```

如果机器人此前使用云端 Bot API，应先停止原来的接收程序，按官方步骤调用云端 `logOut`，再启用本地接入。已有 webhook 冲突时，使用面板“切换为轮询接收”，保留未处理更新。[官方迁移说明](https://github.com/tdlib/telegram-bot-api#moving-a-bot-to-a-local-server)

## 原生后端、Bot API 使用 Docker

原生网站默认读取 `config.yaml` 同目录的 `telegram.yml`。安装脚本会在文件不存在时准备它，并保留已有文件中的挂载配置。手动运行源码且尚无该文件时，在项目根目录执行一次：

```bash
cp deploy/telegram/compose.native.yml backend/telegram.yml
```

在项目根目录准备前述 `.env.telegram`，填写 API 凭据，然后启动：

```bash
sudo install -d -m 750 -o 101 -g 101 /var/lib/telegram-bot-api
docker compose --env-file .env.telegram -f backend/telegram.yml up -d --pull always
```

通过 `install.sh` 安装后，安装目录提供 `telegram.yml` 和 `.env.telegram.example`，在该目录准备 `.env.telegram`，启动命令使用 `-f telegram.yml`。完成部署后重启网站，让它读取 Compose 文件。此示例对应当前以 root 运行的原生后端。Bot API 仅发布到宿主机的 `127.0.0.1:7878`，网站面板填写该地址。

如需使用其他位置的 Compose 文件，在启动网站时通过 `VIDEO_TELEGRAM_COMPOSE` 指定一次文件路径；systemd 部署可在服务环境中设置。该路径属于启动配置，不放入网站 YAML 或 Web 表单。

网站读取配套文件中 `telegram-bot-api` 服务挂载到 `/var/lib/telegram-bot-api` 的数据目录。原生部署使用宿主机目录挂载；支持短格式和 `type: bind` 长格式，相对源目录按 Compose 文件所在目录解析。挂载路径需填写具体值，不使用环境变量插值；API 凭据等其他部署环境变量不参与目录解析。

网站在启动时读取一次挂载关系。文件缺失、挂载重复、只读、使用了原生网站无法访问的命名卷或两端未共享同一份数据时，Telegram 状态及连接检查会明确报错，不回退到其他目录。网站的其他功能仍可使用。

## 更换 TG 数据目录

以原生网站将宿主机数据目录从 `/var/lib/telegram-bot-api` 搬到 `/nzb/tg` 为例：

1. 停止网站和 Bot API，完整复制数据到 `/nzb/tg/`，保留文件权限和所有者，包括 `library/`、Bot API 数据及会话。
2. 将网站实际读取的 `telegram.yml` 中的数据挂载改为 `/nzb/tg:/var/lib/telegram-bot-api`。
3. 重新执行 Compose 启动命令以重建 Bot API，然后启动网站。网站自动读取新挂载关系，无需修改网站配置。
4. 确认已有视频可播放，再验证一次新的视频导入。

已有视频、未完成任务、删除、转存和备份统一使用从 Compose 识别的共享目录下的 `library/`。修改挂载表示整库搬迁，程序不会自动移动文件，也不需要保留旧目录挂载或修改视频记录。

全 Docker 部署迁移时，应从原 `telegram-cache` 卷复制数据，并在 `docker-compose.telegram.yml` 中同时修改网站和 Bot API 两个服务的数据挂载，再重建两个容器。网站自动匹配两个服务挂载的同一数据源，并使用网站容器内的路径。

## 连接已有的 Local Bot API 服务

可以连接其他 Compose 部署的 Local Bot API 服务。提供符合配套结构的 Compose 文件，在其中保留 `telegram-bot-api` 服务和默认容器数据目录挂载，启动网站时用 `VIDEO_TELEGRAM_COMPOSE` 指定实际文件。服务需启用 Local 模式，配置自己的 API ID/API Hash，并允许网站访问 HTTP 端点。

`getFile` 返回服务端的绝对文件路径，网站根据 Compose 挂载关系转换为自己可访问的路径。两端必须访问同一份数据，且网站具有读取、移动文件的权限。仅填写另一台服务器的 API 地址不能满足文件导入要求；本方案不包含无共享存储的远程文件传输。网站读取单份配套文件中的挂载定义，不执行 Compose 的多文件覆盖或目录变量插值。

## 定时转存到网盘

在 **Telegram → 网盘转存** 选择支持上传的目标网盘，并填写相对于该网盘配置根目录的目录，默认 `Telegram`。不选择网盘时只保存在本地。点击“保存转存设置”后自动应用，执行时间沿用 **配置面板 → 定时任务** 的时间和时区；关闭定时任务也会停止自动转存。

需要代理时，在同一面板填写“上传代理”，或设置 YAML 中的 `telegram.upload_proxy`，例如：

```yaml
telegram:
  upload_drive_id: "cloud"
  upload_directory: "Telegram"
  upload_proxy: "socks5h://proxy:1080"
```

支持 `http://`、`https://`、`socks5://`、`socks5h://`，也支持 `http://user:password@proxy:7890` 形式的代理认证；密码中的特殊字符需 URL 编码。`socks5` 在项目端解析目标域名，`socks5h` 由代理解析。代理地址必须能从项目后端访问；Docker 中的 `127.0.0.1` 指向项目容器自身。

该代理用于 TG 转存中的网盘目录创建、文件查询、上传和结果校验，不改变 Bot API 下载、消息接收、普通网盘扫描或播放的网络设置。留空使用网盘驱动的默认网络连接（包括适用的环境代理）。转存设置修改后无需重启，从下一次转存任务生效；当前转存沿用启动时的配置，正在接收或下载的 TG 任务也不会因此重连。代理连接失败时，本轮转存报错并保留本地视频，不回退直连。

转存作为定时流水线的独立阶段，在网盘扫描与本地封面、预览对账之后执行，不依赖爬虫配置。手动“扫描所有网盘”不会触发转存。任务按 TG 导入记录选择仍在本地的视频，包括此前已导入的视频，不会根据可编辑的 `TG` 标签判断来源。

本地视频的来源显示为“本地存储”。确认上传成功、网盘文件大小一致且可取得播放地址后，来源和播放路径一起切换到目标网盘；视频 ID、原页面链接、标签、收藏和点赞记录保留。文件名包含稳定的视频标识，失败后再次执行会检查目标目录中的已有文件，避免重复上传；同名文件大小冲突时保留本地来源并报错。

确认转存成功并保存网盘来源后，自动删除本地原视频，不再提供保留本地副本的选项；封面和预览继续保留在本地。清理只作用于本次转存的文件，不会追溯删除以前保留的副本。若转存成功后本地删除失败，日志会提示人工检查。上传或网盘验证失败时保留本地文件及来源，下次定时任务重试；失败摘要进入定时任务结果，详情记录在 `telegram-upload` 日志中。

首次验证请转发一个较小的视频，确认后台记录变为“已保存”、能打开站内详情，然后再验证超过 20 MB 的视频。再次转发同一文件应返回同一个视频。模拟服务测试不能替代你的真实网络、Telegram 凭据和大文件链路验收。

## 任务、容量与恢复

- 默认单文件上限为 4 GiB，可在后台修改；默认最多 100 个待处理 TG 任务。
- TG 和直链导入共用一个下载 worker；接收消息和回复通知独立运行。
- 新导入的 TG 视频统一附加 `TG` 来源标签，不提供自定义默认标签设置；内容标签继续沿用原有自动匹配规则。历史视频如需补标，由管理员手动处理。
- “正在从 TG 获取”阶段显示不确定进度，随后直接校验和入库。
- 机器人会编辑同一条通知，显示获取、校验、入库和最终结果。下载期间 Bot API 不提供实时字节进度，不显示复制百分比。每条通知两次更新至少间隔 5 秒，内容未变化时不重复发送；较快完成的阶段可能直接跳过。
- 网络等临时失败最多自动重试 3 次；磁盘不足会等待空间恢复。后台支持取消和手动重试。
- 取消只保证项目不再入库，不保证 Bot API 内部下载同时终止。
- 同一机器人接收同一文件时去重。重新上传或重新编码产生的新文件标识不保证去重；现有媒体指纹流程仍独立工作。
- 完成通知表示视频文件和数据库已保存；封面、预览继续后台生成。媒体编码是否能直接播放取决于浏览器，导入过程不转码。
- 项目重启后恢复任务；已经移入 `library/` 的文件直接复用，尚未完成获取的文件重新请求 Bot API，不承诺断点续传。
- 30 天后清理 TG 任务的可下载来源载荷，保留精简任务结果和消息幂等键；过期失败任务需重新转发。

视频下载完成后，项目通过同一文件系统内的重命名，将文件移入共享目录的 `library/` 子目录，校验后直接登记为 TG 本地存储。此步骤不复制文件，不使用普通上传目录，也不会保留下载缓存副本。播放、封面、预览和网盘转存均读取这同一份视频。`library/` 必须与 Bot API 下载目录位于同一文件系统，不能单独挂载另一块盘；无法移动时任务等待处理，不回退到复制。

`library/` 位于机器人各自的缓存目录之外，不参与 Bot API 的自动缓存清理或机器人退出登录时的目录删除。项目只管理已登记的文件：取消或最终校验失败时清理已接管文件；删除原视频或网盘转存成功后释放对应文件。仅从视频列表删除并保留原文件时，仍可从回收记录恢复。Bot API 数据库、会话和其他下载缓存不由项目清理；取消任务不保证 Bot API 内部下载停止，未接管的缓存仍由 Bot API 管理。

下载前只检查共享文件系统可用空间，默认保留空间沿用 `remote_upload.disk_reserve_bytes`（1 GiB），不再要求普通上传目录预留两份视频空间。文件按相对位置登记，关闭接入或更换机器人不会影响已入库文件。修改 Compose 数据挂载并重启网站后，所有 TG 本地视频都会从新位置读取，需先停止两端服务并完成整库搬迁。

只允许一个接收实例使用同一 Token。遇到冲突时先停止另一个实例，再重启当前项目。更换成其他机器人后，旧任务不能使用新机器人身份下载。

站点备份不包含 `config.yaml`，因此不携带 Telegram 配置和凭据；原始消息、通知队列和接收游标也不导出。迁移服务器时需单独保留 `config.yaml`、实际使用的 Compose 文件和 Bot API 的部署凭据文件 `.env.telegram`；包含本地上传资源时会同时备份已入库的 TG 视频及有效的文件去重映射；只收集登记的视频，不导出 Bot API 缓存、数据库或主机文件路径。恢复的 TG 视频放入普通本地视频库，保留 TG 来源记录，并可继续转存；不会写入目标 Bot API 的运行目录。恢复后暂停机器人接收并清空旧通知；目标实例没有凭据时先在面板重新配置，再点击“检查连接并恢复接收”后继续。Telegram 未消费更新最多保留 24 小时，因此长时间离线后应检查近期转发是否都有导入记录。[更新接收说明](https://core.telegram.org/bots/api#getting-updates)
