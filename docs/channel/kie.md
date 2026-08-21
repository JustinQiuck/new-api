# KIE AI 渠道接入

KIE AI 渠道用于把 KIE 的文本、图片和视频模型接入 New API。客户端只使用 New API 用户令牌，KIE API Key 仅保存在服务端渠道配置中。

当前接入属于内部测试版本。在完成真实账号调用、失败退款和商业使用条款确认前，不应直接向付费用户开放。

## 创建渠道

1. 在管理后台新增渠道，类型选择 `KIE AI`。
2. Base URL 保持默认值 `https://api.kie.ai`。
3. 在渠道密钥中填写 KIE API Key，不要把该 Key 写入前端环境变量、仓库或客户端配置。
4. 为渠道启用需要开放的公开模型。

| 公开模型名 | KIE 上游模型 | 接口 |
| --- | --- | --- |
| `gpt-5.5` | `gpt-5-5` | `/v1/responses` |
| `gpt-image-2` | `gpt-image-2-text-to-image` / `gpt-image-2-image-to-image` | `/v1/images/generations`、`/v1/images/edits` |
| `grok-imagine-video` | `grok-imagine-video-1-5-preview` | `/v1/videos`、`/v1/videos/:task_id` |

模型映射只发生在服务端。客户端继续使用公开模型名，不能直接使用 KIE Key。

## FreeCanvas 配置

在 FreeCanvas 中配置：

- Base URL：部署后的 New API 地址；
- API Key：New API 为该用户签发的令牌；
- 文本模型：`gpt-5.5`；
- 图片模型：`gpt-image-2`；
- 视频模型：`grok-imagine-video`。

FreeCanvas 当前的文本、图片和视频请求协议无需修改。音频不属于本轮 KIE 渠道能力，应继续使用其他渠道。

## 视频参数与计费

视频适配器接受 KIE 当前公开 schema 中的参数：

- 时长：1–15 秒，缺省为 8 秒；
- 分辨率：`480p`、`720p`、`1080p`；
- 比例：`auto`、`1:1`、`16:9`、`9:16`、`3:2`、`2:3`；
- 参考图：最多 7 张；1080p 最多 1 张。

视频预扣以后台配置的 `grok-imagine-video` 模型价格为基准，再应用时长和分辨率倍率。当前分辨率倍率为 480p `1`、720p `1.5`、1080p `2.25`。这里的价格是面向用户的独立售价，不等同于 KIE 成本。

KIE 返回的 `creditsConsumed` 不参与用户扣费，也不会写入公开任务数据。异步任务失败时沿用 New API 的终态 CAS 与退款链路，避免重复轮询造成重复退款。

## 数据与安全边界

- 用户提示词和参考素材会被发送给 KIE，产品隐私说明中应明确披露第三方处理。
- 图片编辑和图生视频会先把参考图上传到 KIE 临时文件服务。
- 图片请求使用 `b64_json` 时，New API 会下载临时结果并返回 Base64；视频结果 URL 应由客户端尽快下载并保存。
- 上游真实任务 ID 保存在任务私有数据中；客户端只看到 New API 生成的 `task_...` ID。
- 轮询日志和公开 Task Data 会剥离上游任务 ID、请求参数、失败原文和 `creditsConsumed`。
- KIE Key 应设置消费上限、最小权限和定期轮换，并限制数据库及备份访问。

## 上线前验收

使用真实 KIE 测试 Key 按顺序验证：

1. `/v1/models` 能看到三个公开模型名。
2. `/v1/responses` 的流式和非流式请求均成功。
3. 文生图和带参考图编辑均能返回可持久化结果。
4. 文生视频和带参考图视频均能创建公共任务 ID，并最终返回可下载 URL。
5. 人为制造一次上游失败，确认用户余额只退款一次。
6. 检查浏览器网络记录、普通日志和任务接口，确认没有 KIE Key、真实任务 ID、请求参数或 `creditsConsumed`。
7. 确认 KIE 对转售、白标或代充场景的有效条款或书面许可。

代码测试通过不代表真实 KIE 账号、生产代理、数据库和商业授权已经验收。
