# Aurora Agentic System

## Project Structure
doc
|- design 设计文档，你可以通过它们了解系统的设计思路，系统架构以及开发Spec。
|- progress 开发进度记录，研发时可以回顾之前开发内容。
|- dev 开发调试指引

生成文档时需要按照类别写入特定目录。

## Plan
第一阶段我们只需要实现核心的框架，一些LLM调用接口可以先实现逻辑，数据可以mock。TS Worker可以简单实现几个demo，不需要模拟函数计算的serverless环境。但是核心的dag流转，调度器核心逻辑，记忆管理（GraphRAG，滚动压缩，内部查询Kvrocks输出的Skill）等需要把核心逻辑实现。
该阶段的测试可以对一些未实现的组件进行mock，或者实现最小可测试单元。

第二阶段我们需要细化，把一些上一个阶段mock的组件逐个完善。

## dev enviroment
初期开发环境搭建尽量保证在macos m2本机环境可以运行demo，对组件的依赖可以先以来docker compose方式搭建最小可用版本。初期先保证我可以在本机环境方便开发调试。
我的本机环境是Goland，VSCode等编辑器，我希望能在这些IDE中进行可视化的断点调试debug，在生成工程的时候希望能生成VSC需要的配置文件。针对Rust/Go/TS等语言环境进行配置。

## Spec
在项目研发过程中，需要生成必要的编辑器配置，各语言lint，format的配置文件，我们严格遵循Google Style来进行代码风格检查以及代码的格式化。
在Git提交项目时需要遵循Git Commit提交规范（参考大型开源项目的提交规范）。
我们默认采用main分支进行开发，远程仓库提交请参考
```shell
git remote add origin git@github.com:linuxb/Aurora.git
git branch -M main
git push -u origin main
```
在完成一个模块单元（可以生成对应的测试用例）完成单元测试后，即可进行单个小模块的提交。