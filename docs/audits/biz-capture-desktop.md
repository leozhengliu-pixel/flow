# Business Desktop Capture Handoff

交付目录：`/tmp/codex-biz-capture-desktop/deliver/`；完整说明、安装命令、逐屏 want/got、截图、工具补丁和原始结果均在该目录。产品代码未修改；正式 gate、正式参考目录、手机文件和 accepted 标记未写入。

交付清单：`deliver/MANIFEST.json`，SHA-256 为 `33aefc441658cc3a6f6f2cdd30df450c73346cd75b8681f1a67f2d00668e0214`。

- gate 从 159 屏扩展为 166 屏：新增 3 个设置页和 4 个 Insights 状态，替换 4 个已有参考。
- Asks、Initiatives 旧 PNG 经逐像素及 SHA 校验后复用；Customer requests 和其他要求的状态使用新捕获，测量 JSON 使用当前稳定 DOM。
- 完整删除 `referenceShowsInsights` 和对应 CSS；新增只读数据夹具、卡片/面板几何检查、路由核验，以及规范 Timeline 路径。
- 最终目标验证提交为 `c717dcacd79b8d5faae5000757c1dd87a0ac70c5`：正式 4 屏基线 3 PASS / 1 FAIL；7 个状态原未入 gate。最终 11 屏均 FAIL，202 项差异，0 INCONCLUSIVE。聚合来源明确列在 `results/target-final.json`。
- 历史完整 sweep 位于 `results/full.*`，提交 `3db677f5`，早于最后 Timeline 路由修订：112 PASS、44 FAIL、9 INCONCLUSIVE、1 计费页 NO_REFERENCE，289 项差异。没有冒充最终 main 的全量结果。
- 删除隐藏 CSS 使另外 13 个旧 Free 参考新增 25 项坐标差异；正式 gate 补跑证据及分类在 `REGRESSION-CHECK.md`，未擅自重拍任务范围外的参考。
- 安装副本补丁复现、语法检查和 166 屏索引重建 exit 0；API 构建和 npm ci exit 0。浏览器 gate exit 1，保留真实产品差异；2px mutation proof 通过并恢复。完整命令在 `TESTS.md`。

已逐张打开最终参考和 Flow 截图。复核入口、各图内容和差异在 `VISUAL-REVIEW.md`：三个设置路径、团队 Active → Insights → 全屏或 Measure/Slice/Segment，以及新 issue/project view 的展开、折叠和 Year 时间轴。

覆盖浅色、英文、1440×900@2，包含菜单打开/选中、禁用和分组折叠状态；没有声称覆盖深色、zh-CN、375/390 或完整 hover/focus-visible 矩阵。本任务没有产品控制器变更。

参考业务数据未写入，21 个自有标签页均关闭；临时面板和布局状态恢复。自有服务按 PID 停止，安装的 node_modules 在交付前移除。
