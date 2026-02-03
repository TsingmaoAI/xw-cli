![](/images/xuanwu.png)
<center> <b>玄武CLI</b>｜<b>xw-cli</b> </center>

<center> 实现国产算力的大模型自由，打造国产版Ollama </center>

为多元国产算力平台深度优化模型性能。告别繁琐的环境配置与算子适配，一条命令启动生产级服务，即刻释放大模型潜能。

## 里程碑

- **2026.02.02** 玄武CLI正式开源，并支持华为Ascend系列芯片。

## ✨ 特性

### 🇨🇳 国产原生
- 深度优化国产硬件性能
- 本地化完整支持

### ⚡ 低门槛，一键启动
- 无需复杂配置，开箱即用
- 自动硬件检测与引擎推荐

### 🔧 多引擎支持
- 内置多个推理引擎适配与自动路由
- 保证性能与模型覆盖广度

## 📦 安装

### 前置要求
- 拥有linux系统
- 拥有受[支持的国产卡](https://xw.tsingmao.com/hardware.html)，并确认驱动已正确安装

### 极速安装

```bash
curl -o- http://xw.tsingmao.com/install.sh | bash
```

## 🚀 快速开始

### 1. 启动xw-cli server

```bash
xw serve
```

### 2. 拉取模型

```bash
xw pull qwen3-8b
```

更多模型详见 [xw 模型仓库](https://xw.tsingmao.com/models.html)。

### 3. 运行模型

```bash
xw run qwen3-8b
```

开始对话，输入 `/quit` 退出。

### 4. 查看本地模型

```bash
# 本地存储的模型
xw ls

# 正在运行的模型
xw ps
```

## 📚 文档

- **官网**: [xw.tsingmao.com](https://xw.tsingmao.com/)
- **文档**: [xw.tsingmao.com/doc](https://xw.tsingmao.com/doc.html)
- **模型仓库**: [xw.tsingmao.com/models](https://xw.tsingmao.com/models.html)

## 🐛 反馈

如遇到问题或有建议，欢迎 [提交 Issue](https://github.com/TsingmaoAI/xw-cli/issues)

## 💬 社区交流

欢迎加入 xw-cli 微信交流群，与开发者和用户一起讨论技术问题、分享使用经验：

<table>
  <tr>
    <td align="center">
      <img src="images/wx_4.jpg" width="200px" alt="微信群4"/><br/>
      <sub><b>微信群 4</b></sub>
    </td>
    <td align="center">
      <img src="images/wx_5.jpg" width="200px" alt="微信群5"/><br/>
      <sub><b>微信群 5</b></sub>
    </td>
    <td align="center">
      <img src="images/wx_6.jpg" width="200px" alt="微信群6"/><br/>
      <sub><b>微信群 6</b></sub>
    </td>
  </tr>
</table>

*如群二维码失效，请通过 Issue 联系我们*

---

本项目由清昴智能团队维护。更多信息访问 [清昴智能官网](https://www.tsingmao.com/)
