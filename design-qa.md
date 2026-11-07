# Product Design 验收：原生工作台扩展

final result: passed

范围：已渲染的「今日」、通用资源库、注册/编辑/共享/预览/下载、HTTP 来源映射和手机布局。此结论不等于生产切换或客户实际发送验收。

## 比较依据和状态

- Source visual truth：`.var/native-acceptance/native-automation-source.png`，官方固定 Harness 的自动化页面，1440×1000 CSS px / 实际1440×1000，deviceScaleFactor=1。
- 它是原生布局、字体、颜色、侧栏、按钮的视觉基线；资源表单与今日属于新增业务状态，内容和信息密度有意不同，不能宣称逐像素复刻同一页面。
- Rendered：`.var/native-acceptance/today-desktop.png`、`resource-form-desktop.png`、`resource-preview-desktop.png`、`nested-name-preview.png`，均1440×1000；`resource-preview-mobile.png`、`http-source-form-mobile.png`均390×844，CSS与输出像素一致，density=1，无浏览器外框。
- 全景证据：同次图片输入中并列打开官方基线、桌面表单、桌面预览及手机预览；修复后又共同打开基线、手机映射表单及嵌套名称预览，最后基线与今日共同打开核对。
- 控件/字体/正文在上述1倍截图内清晰可读，无需再裁切放大才能判断。原生侧栏未重写；390px时使用上游折叠行为。

## 发现、修复及复验

1. P2 下载链接随旁边选择框被拉高。改为`.wb-actions{align-items:flex-start}`，后续手机预览已保持单行按钮高度。
2. P2 表单副标题继承默认h2过大。明确16px/500，与页面20px主标题形成层次；`resource-form-desktop-before.png`保留修前，修后表单截图核对通过。
3. P2 资源尚未读回时能点注册，可能展示不完整连接。按钮等待catalog且操作busy时禁用；失败保留提示，成功清除旧错误。
4. P2 合法对象型name会导致React预览报错。标题只接受string/number，其他回退记录编号；真实页面发布`name:{zh:…}`的合成记录，展开`nested-name`及JSON正文成功，零pageerror。
5. P2 表单包裹标签会把选项/textarea正文加入控件名称，精确定位与读屏名称不稳定。控件增加显式aria-label；手机HTTP映射配置及错误JSON提交后继续操作通过。

## 五项视觉检查

- 字体：复用官方系统字体和fallback，14px正文、20px页面标题、16px分区；字重和换行清楚，无另外下载字体。
- 间距：内容最大960px、响应式边距、两列在手机变一列；按钮自然换行，390px无水平溢出，信息可纵向滚动。
- 颜色：复用官方背景、主次文字、边框token；已修不存在的边框token，无额外品牌色。
- 资产：侧栏和图标使用上游图标库与原生资产；无手绘替代品牌资产，无新增栅格装饰。
- 文案：区分资料发布、执行状态、渠道受理/待手机确认/已发送/已送达；资源板块、共享范围和发布日期明确。验收库的英文任务标题属于合成测试数据，不会迁入新生产库。

## 真实交互覆盖

隔离Playwright（用户已授权），实际官方Harness页面：注册资源→公开/内部两板块→只勾选项目公开板块→发布合成记录→查看→下载确认不包含内部字段；HTTP来源选取、JSON Pointer映射、错误JSON保留表单；对象型标题预览；今日和资源导航。控制台pageerror为0，桌面/手机无水平溢出。真实附件上传/专家读取及真实今日派发回传另见`native/ACCEPTANCE.md`。

不把页面可点击视为后台恢复、权限或外发验收；这些由真实MySQL/Temporal和渠道回执分别证明。新生产环境仍须对安装包做同入口读回。
