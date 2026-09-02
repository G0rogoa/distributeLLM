# Requirements

使用远端现有 Conda 环境，不为 tokenizer sidecar 创建新环境。

需要：

- Python 3.11 或更新版本
- `transformers`
- 本地已经存在的 tokenizer/model 目录

远端示例：

```bash
/opt/anaconda3/bin/conda run -n zsq python -c "import transformers; print(transformers.__version__)"
```

服务使用 `AutoTokenizer.from_pretrained(..., local_files_only=True, trust_remote_code=False)`。如果本地目录不存在或缺 tokenizer 文件，服务启动失败。
