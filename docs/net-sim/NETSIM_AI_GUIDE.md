# net-sim 配置与热更新指南（供 AI / 自动化测试使用）

> 读者：按测试需求自动生成 net-sim 配置、热切换配置、再执行测试的 AI 或脚本。
> 适用版本：mihomo `net-sim-v2`（Android App 2.16.1：`com.github.metacubex.clash.meta` 或独立包 `com.github.metacubex.clash.netsim`；Linux `mihomo-linux-amd64/arm64`）。
> 人类阅读版说明：`docs/net-sim/README.md`。

---

## 0. 必须遵守的 6 条规则

1. **所有数值都是"客户端测量到的往返结果"**：`latency: 200` 表示 RTT 增加 200ms，`loss: 5` 表示 ping 式往返丢包 5%。不要自己乘 2 或除 2（换算规则见第 3 节）。
2. **热更新使用 REST API**：`PUT /net-sim`（全量替换）或 `PATCH /net-sim`（只改传入字段）。不需要重启内核，也不需要重载配置。
3. **PUT 的 body 必须带 `"enabled": true`**，否则等于关闭。未写的字段一律变成 0（关闭）。
4. **不要用 `DELETE /net-sim` 作为"清零"**：DELETE 会把 `enabled` 设为 false，之后新建的连接不再经过模拟器，后续热更新对它们无效。要清零请用 `PUT {"enabled": true}`。
5. **每次切换配置后**：先 `GET /net-sim` 确认生效，再 `DELETE /net-sim/stats` 清空计数，然后等待"排空时间"（第 6 节），才开始下一轮测试。
6. **只有经过 mihomo 隧道的流量会被模拟**（TUN / mixed / socks / http 入站）。由内核直接应答的 DNS（`dns-hijack`）、目标为 DIRECT 以外的特殊处理流量不受影响。

---

## 1. 配置项完整说明

| 字段 | 类型 | 单位 | 合法范围 | 默认 | 含义（客户端实测口径） |
|---|---|---|---|---|---|
| `enabled` | bool | - | true / false | false | 总开关。为 true 时所有新连接都会经过模拟器（即使其它值全为 0），之后的热更新对这些连接立即生效 |
| `latency` | int | ms | ≥ 0 | 0 | 每次往返的 **RTT 增加量**。内部上下行各加一半 |
| `jitter` | int | ms | ≥ 0 | 0 | RTT 在 `latency ± jitter` 内随机波动。建议 `jitter ≤ latency`，否则下限被截到 0，平均值会偏高 |
| `loss` | float | % | 0 ~ 100 | 0 | **往返丢包率**（等同 ping 的 packet loss）。UDP 直接丢包；TCP 表现为重传停顿（数据完整，只是变慢、卡顿）；`100` 表示断网：UDP 全丢，TCP 冻结，降低 loss 后恢复 |
| `bandwidth` | float | Mbps | ≥ 0 | 0 | 上行和下行**各自**的吞吐上限。1 Mbps = 1,000,000 bit/s = 125,000 B/s。所有连接（TCP+UDP）共享 |
| `upload-bandwidth` | float | Mbps | ≥ 0 | 0 | 上行（手机 → 服务器）上限，>0 时覆盖 `bandwidth` 的上行部分 |
| `download-bandwidth` | float | Mbps | ≥ 0 | 0 | 下行（服务器 → 手机）上限，>0 时覆盖 `bandwidth` 的下行部分 |
| `corruption` | float | % | 0 ~ 100 | 0 | 往返后内容被改动的 **UDP** 包比例（每个坏包随机翻转 1 bit）。TCP 上等同 loss（校验失败后重传） |
| `duplication` | float | % | 0 ~ 100 | 0 | 往返后收到重复的 **UDP** 包比例。TCP 无可见效果（协议栈去重） |
| `reordering` | float | % | 0 ~ 100 | 0 | 往返后晚于后发包到达的 **UDP** 包比例。需要连续发包（间隔 < 50ms）才会体现。TCP 无可见效果 |
| `burst-loss` | int | 个 | ≥ 0 | 0 | ≥2 时丢包成串出现，每串连续丢 N 个；**平均丢包率仍等于 `loss`**。需要 `loss > 0` 才生效；0 或 1 表示均匀随机丢包 |
| `burst-delay` | int | ms | ≥ 0 | 0 | 链路随机进入"坏时段"（约 5% 的包触发，平均持续约 3 个包），期间 RTT 额外增加 0 ~ `burst-delay`。与 `loss` 无关，可单独使用 |
| `packet-size` | int | bytes | 0 或 ≥ 68 | 0 | 路径 MTU。大于该值的 UDP 包被丢弃（模拟 MTU 黑洞）；TCP 以 `packet-size - 40` 作为分段大小计算丢包概率 |
| `queue-type` | string | - | `fifo` / `prio` / `fair-queue` | `fifo` | 预留字段，目前都按 fifo 执行，可以省略 |

### 校验规则（不满足时 API 返回 HTTP 400，配置不变）

- `loss` / `corruption` / `duplication` / `reordering` 必须在 `[0, 100]` 内。
- `latency` / `jitter` / `burst-loss` / `burst-delay` / 各带宽不能为负数。
- `packet-size` 只能是 0 或 ≥ 68。
- `queue-type` 只能是上面三种之一，或为空。
- YAML 配置文件中写了非法值时，内核打印 error 日志，并以关闭状态启动 net-sim。

### 字段之间的关系

- 带宽优先级：`upload-bandwidth` / `download-bandwidth` > `bandwidth`。
- `burst-loss` 依赖 `loss`；`burst-delay` 独立于其它字段。
- `latency`、`jitter`、`burst-delay` 三者叠加；`loss` 和 `corruption` 在 TCP 上一起决定重传停顿。
- TCP 每次重传停顿一个 RTO：`RTO = max(200ms, latency + 2 × jitter)`。

---

## 2. 示例配置

### 2.1 写在 profile（YAML）中：测试前的基线

```yaml
external-controller: 0.0.0.0:9090   # 热更新 API；Android 用 adb forward 访问时可写 127.0.0.1:9090
secret: ""                          # 设置后，API 请求需带 Authorization: Bearer <secret>
net-sim:
  enabled: true                     # 推荐常开：值全为 0 时没有任何效果，但能保证热更新覆盖所有连接
```

### 2.2 全字段示例

```yaml
net-sim:
  enabled: true
  latency: 200            # RTT +200ms
  jitter: 50              # RTT 在 150~250ms 之间波动
  loss: 1.0               # 往返丢包 1%
  bandwidth: 10           # 上下行各 10 Mbps
  upload-bandwidth: 5     # 上行改为 5 Mbps
  download-bandwidth: 15  # 下行改为 15 Mbps
  corruption: 0.5         # 0.5% 的 UDP 包损坏
  duplication: 0.2        # 0.2% 的 UDP 包重复
  reordering: 5.0         # 5% 的 UDP 包乱序
  burst-loss: 2           # 丢包两个一组出现，平均仍为 1%
  burst-delay: 100        # 坏时段 RTT 额外 +0~100ms
  packet-size: 1500       # MTU 1500
  queue-type: fifo
```

对应的 JSON（用于 API）：

```json
{"enabled":true,"latency":200,"jitter":50,"loss":1.0,"bandwidth":10,"upload-bandwidth":5,"download-bandwidth":15,"corruption":0.5,"duplication":0.2,"reordering":5.0,"burst-loss":2,"burst-delay":100,"packet-size":1500,"queue-type":"fifo"}
```

### 2.3 场景模板（可直接作为 PUT body）

| 场景 | JSON |
|---|---|
| 理想网络（清零） | `{"enabled":true}` |
| 4G 良好 | `{"enabled":true,"latency":50,"jitter":10,"loss":0.1,"upload-bandwidth":10,"download-bandwidth":30}` |
| 4G 一般 | `{"enabled":true,"latency":100,"jitter":30,"loss":0.5,"upload-bandwidth":2,"download-bandwidth":5}` |
| 3G | `{"enabled":true,"latency":200,"jitter":50,"loss":1,"upload-bandwidth":0.5,"download-bandwidth":1.5}` |
| 2G / EDGE | `{"enabled":true,"latency":600,"jitter":150,"loss":2,"upload-bandwidth":0.1,"download-bandwidth":0.2}` |
| 电梯 / 地库（断续） | `{"enabled":true,"latency":300,"jitter":100,"loss":5,"burst-loss":5,"burst-delay":800,"upload-bandwidth":0.5,"download-bandwidth":1}` |
| 上行塌陷 | `{"enabled":true,"latency":150,"jitter":50,"loss":2,"burst-loss":3,"upload-bandwidth":0.3,"download-bandwidth":6}` |
| 跨境高延迟 | `{"enabled":true,"latency":250,"jitter":20,"loss":0.3}` |
| 弱 WiFi（乱序、重复） | `{"enabled":true,"latency":40,"jitter":40,"loss":3,"reordering":3,"duplication":1}` |
| 完全断网 | `{"enabled":true,"loss":100}` |
| 小 MTU（隧道 / VPN 场景） | `{"enabled":true,"packet-size":1280}` |

---

## 3. 需求描述 → 配置值换算规则

| 需求里的说法 | 对应配置 |
|---|---|
| "延迟 / RTT / ping 增加 X ms" | `latency: X` |
| "单向延迟 X ms" | `latency: 2X` |
| "延迟 X ± Y ms" / "抖动 Y" | `latency: X, jitter: Y` |
| "延迟 X~Z ms 之间" | `latency: (X+Z)/2, jitter: (Z-X)/2` |
| "丢包率 X%"（未说明方向，默认按 ping 口径） | `loss: X` |
| "单向丢包 X%" | `loss: 100 × (1 − (1 − X/100)²)`（X 较小时约为 2X） |
| "带宽 / 限速 X Mbps" | `bandwidth: X` |
| "X kbps" | `X / 1000` Mbps |
| "X KB/s"（1 KB = 1000 B） | `X × 8 / 1000` Mbps，例如 500 KB/s → 4 |
| "X KiB/s"（1 KiB = 1024 B） | `X × 8.192 / 1000` Mbps，例如 512 KiB/s → 4.194 |
| "X MB/s" | `X × 8` Mbps |
| "上行 A、下行 B" | `upload-bandwidth: A, download-bandwidth: B` |
| "突发 / 连续丢包，每次丢 N 个" | `loss: <平均丢包率>, burst-loss: N` |
| "偶发卡顿 / 延迟尖刺最高 +X ms" | `burst-delay: X` |
| "断网 / 无网络" | `loss: 100` |
| "MTU X" | `packet-size: X` |
| "包损坏 / 重复 / 乱序 X%" | `corruption` / `duplication` / `reordering: X`（只对 UDP 可见） |

没有提到的字段一律不写；用 PUT 时它们会被置 0。

---

## 4. 热更新 REST API

Base URL（以下记为 `$API`）：

- Linux 服务器：`http://<host>:<external-controller 端口>`
- Android：先执行 `adb forward tcp:19090 tcp:9090`，之后 `API=http://127.0.0.1:19090`；同一局域网内也可以直接访问 `http://<手机 IP>:9090`（需要 profile 中 `external-controller: 0.0.0.0:9090`）

设置了 `secret` 时，每个请求都要加 `-H "Authorization: Bearer <secret>"`。

| 方法 | 路径 | Body | 作用 | 成功返回 |
|---|---|---|---|---|
| GET | `/net-sim` | - | 读取当前配置 | 配置 JSON |
| PUT | `/net-sim` | 完整配置 JSON | **全量替换**，未写字段 = 0 | `{"message":"net-sim updated","config":{...}}` |
| PATCH | `/net-sim` | 部分字段 JSON | 只修改传入的字段，其余保持不变 | `{"message":"net-sim patched","config":{...}}` |
| DELETE | `/net-sim` | - | 全部清零并 `enabled=false`（测试结束时才用） | `{"message":"net-sim disabled and reset"}` |
| GET | `/net-sim/stats` | - | 读取统计计数 | 计数 JSON |
| DELETE | `/net-sim/stats` | - | 清空统计计数 | `{"message":"stats reset"}` |

非法参数返回 `400 {"error":"..."}`，当前配置保持不变。

```bash
API=http://127.0.0.1:19090
H='Content-Type: application/json'

curl -s -X PUT   $API/net-sim -H "$H" -d '{"enabled":true,"latency":200,"loss":2}'   # 全量切换
curl -s -X PATCH $API/net-sim -H "$H" -d '{"loss":5}'                               # 只改 loss
curl -s          $API/net-sim                                                       # 确认
curl -s -X DELETE $API/net-sim/stats                                                # 清计数
curl -s          $API/net-sim/stats                                                 # 读计数
curl -s -X PUT   $API/net-sim -H "$H" -d '{"enabled":true}'                         # 清零（保持 armed）
```

---

## 5. 热更新的生效规则

| 情况 | 是否立即生效 |
|---|---|
| `enabled: true` 期间建立的 TCP 连接和 UDP 会话 | ✅ 立即生效：下一个数据块 / 数据包就使用新配置（已验证：同一条连接上 0 → 200 → 0ms 即时切换） |
| `enabled: false` 期间建立的连接 | ❌ 永远不会被模拟，需要断开重连（重启被测 App 或等它重建连接） |
| 带宽限制 | ✅ 立即生效（令牌桶重建，所有连接共享） |
| 已在"路上"的数据 | 仍按旧配置的延迟送达；切换后等待排空时间（第 6 节） |
| `loss: 100`（断网） | TCP 冻结、UDP 全丢；PATCH 降低 loss 后，TCP 从断点继续，数据完整 |
| 统计计数 | 不会自动清零，每轮测试前手动 `DELETE /net-sim/stats` |

因此最可靠的做法是：**profile 中常驻 `net-sim: {enabled: true}`，测试中只用 PUT / PATCH 切换，不用 DELETE。**

---

## 6. 标准测试循环（AI 按此执行）

```
对每个测试用例 case：
  1. 把需求换算成 JSON 配置（第 3 节）→ cfg，必须包含 "enabled": true
  2. PUT  $API/net-sim  body=cfg
       - HTTP 400 → 读取 error，修正配置后重试，不要继续测试
  3. GET  $API/net-sim  → 逐字段比对 cfg，不一致则重试第 2 步
  4. 等待排空时间 = max(1s, 上一轮的 latency + jitter + burst-delay 毫秒数)
     若上一轮是 loss: 100，额外再等 1s
  5. DELETE $API/net-sim/stats
  6. 执行本轮测试（尽量新建连接；长连接也会生效）
  7. GET $API/net-sim/stats → 记录下来，作为"模拟确实生效"的证据（第 7 节）
  8. 进入下一个 case（直接 PUT 新配置，不需要先清零）
全部结束：PUT {"enabled":true}（恢复理想网络），或 DELETE $API/net-sim（彻底关闭）
```

可直接使用的 bash 函数：

```bash
API=${API:-http://127.0.0.1:19090}
AUTH=(); [ -n "$SECRET" ] && AUTH=(-H "Authorization: Bearer $SECRET")

netsim_apply() {           # 用法: netsim_apply '{"enabled":true,"latency":200}' [上一轮排空秒数]
  local cfg="$1" drain="${2:-1}"
  local code; code=$(curl -s -o /tmp/netsim_resp -w '%{http_code}' -X PUT "$API/net-sim" \
        -H 'Content-Type: application/json' "${AUTH[@]}" -d "$cfg")
  if [ "$code" != "200" ]; then echo "net-sim PUT failed ($code): $(cat /tmp/netsim_resp)" >&2; return 1; fi
  sleep "$drain"
  curl -s -X DELETE "$API/net-sim/stats" "${AUTH[@]}" >/dev/null
  echo "applied: $(curl -s "$API/net-sim" "${AUTH[@]}")"
}
netsim_stats() { curl -s "$API/net-sim/stats" "${AUTH[@]}"; echo; }
netsim_reset() { netsim_apply '{"enabled":true}' "${1:-1}"; }

# 示例
netsim_apply '{"enabled":true,"latency":200,"jitter":50}'
#   ... 跑测试 ...
netsim_stats
netsim_apply '{"enabled":true,"loss":5,"burst-loss":3}' 1   # 上一轮 200+50ms < 1s，排空 1s 即可
#   ... 跑测试 ...
netsim_stats
netsim_reset
```

---

## 7. 判断模拟是否生效（stats 字段）

| key | 含义 | 什么配置下应该 > 0 |
|---|---|---|
| `total-packets` | 经过模拟的 UDP 包 + TCP 数据块（每个方向各计一次） | 任一效果非 0 且有流量时 > 0；为 0 说明流量没走隧道，或连接建立于 `enabled:false` 时（值全为 0 时是直通，不计数） |
| `upload-packets` / `download-packets` | 按方向拆分 | 同上 |
| `delayed-packets` | 被延迟的包 / 块 | latency / jitter / burst-delay |
| `dropped-packets` / `upload-dropped` / `download-dropped` | 丢弃的 UDP 包或 TCP 分段 | loss / packet-size |
| `burst-drops` | 其中属于突发丢包的部分 | burst-loss |
| `burst-delays` | 处于延迟坏时段的包 / 块 | burst-delay |
| `tcp-retransmits` | TCP 重传停顿次数 | loss / corruption（有 TCP 流量时） |
| `corrupted-packets` | 损坏的包（TCP 为校验失败的分段） | corruption |
| `duplicated-packets` | 重复的 UDP 包 | duplication |
| `reordered-packets` | 乱序的 UDP 包 | reordering |
| `oversize-drops` | 超过 packet-size 被丢弃的 UDP 包 | packet-size |
| `throttled-bytes` | 经过限速桶的字节数 | 任一带宽字段 |

### 自行验证数值的方法

| 字段 | 方法 | 预期 |
|---|---|---|
| latency / jitter | 同一条连接上多次请求-应答（TCP echo、HTTP keep-alive、UDP echo），算 RTT | 中位数 ≈ 基线 + latency；min / max 落在 ±jitter 内 |
| bandwidth | 下载 / 上传约 3 秒的数据量，字节数 × 8 / 秒数 | ≈ 配置值（误差约 1%） |
| loss | 发 ≥ 3000 个 UDP echo，统计未返回的比例 | ≈ loss（3000 个样本时随机误差约 ±1%） |
| corruption / duplication / reordering | UDP echo，比对内容 / 计数 / 序号 | ≈ 配置值 |
| packet-size | 分别发大于和小于该值的 UDP 包 | 大包全部丢失，小包全部返回 |

现成的自检脚本：`docs/net-sim/scripts/netsim_e2e.py` 会逐项设置并打印"配置值 vs 实测值"（见 README）。

---

## 8. 备用方式：修改 profile 文件并重载（不推荐用于频繁切换）

修改 YAML 中的 `net-sim:` 段后重载配置。缺点是会重启核心，所有连接断开，耗时 1~3 秒。

```bash
P=com.github.metacubex.clash.meta        # 独立包为 com.github.metacubex.clash.netsim
adb shell am start -n $P/com.github.kr328.clash.ExternalControlActivity -a $P.action.REFRESH_PROFILE
# 如需完全重启：
adb shell am start -n $P/com.github.kr328.clash.ExternalControlActivity -a $P.action.STOP_CLASH
adb shell am start -n $P/com.github.kr328.clash.ExternalControlActivity -a $P.action.START_CLASH
```

Linux 服务器上，修改 `config.yaml` 后执行 `curl -X PUT $API/configs?force=true -d '{"path":"/path/config.yaml"}'`，或重启进程。
注意：重载配置会用文件里的 `net-sim` 覆盖之前通过 API 设置的值。

---

## 9. 已知限制

- **新 TCP 连接少一个 RTT**：TUN 在本机完成三次握手，所以一次全新的 HTTP 请求约增加 1 × latency（真实网络约 2 × RTT）。复用连接和 UDP 不受影响。
- TCP 上的 duplication / reordering 对应用不可见，这与真实网络一致。
- TCP `loss: 100` 时，连接建立（本机握手）仍会成功，只是没有数据往来；App 的超时由它自己的计时器决定。
- reordering 需要同一会话内持续发包（间隔 < 50ms），稀疏包不会被乱序。
- 概率类指标是统计结果，样本少时有波动；判定时建议 ≥ 3000 个样本，或放宽到 ±1~2%。
- `dns-hijack` 的 DNS 查询不受影响；如需模拟 DNS 延迟，使用 `POST $API/dns/delay?delay=<ms>`。

---

## 10. 排错

| 现象 | 原因 / 处理 |
|---|---|
| `total-packets` 始终为 0 | 当前配置所有值都是 0（直通不计数）；或流量没走隧道（检查 App 是否在 VPN 中、规则是否命中），或连接建立于 `enabled:false` 时（重启被测 App） |
| API 连接被拒 | 检查 profile 中的 `external-controller`，Android 需要 `adb forward`；确认当前运行的是正确的包（meta / netsim） |
| 401 Unauthorized | 需要 `Authorization: Bearer <secret>` |
| 400 Bad Request | 参数越界，看返回的 `error` 字段 |
| 改了配置没效果 | 是否用了 PUT 但没带 `"enabled": true`？是否之前调用过 DELETE，导致现有连接未被包装？ |
| 延迟比预期大 | 上一轮的高延迟数据还没排空，按第 6 节等待；或 jitter 大于 latency 导致均值偏高 |
| profile 重载后 API 设置的值丢失 | 正常现象：重载会使用文件里的 `net-sim` |
