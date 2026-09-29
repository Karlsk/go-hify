<template>
  <!-- MCP Server 工具清单抽屉：子资源标准形态（对齐 ProviderModelsDrawer 先例，不增路由）。
       工具来自最近一次「发现工具」的入库结果（DB 读，不连 server）；发现 = 连通测试 +
       tools/list + upsert 一个动作（HTTP 200 + success 字段——失败是业务结果）；
       运行 = 实调工具做管理面诊断（不检查 server 启停）。 -->
  <el-drawer v-model="visible" :title="title" size="560px" class="tools-drawer">
    <div class="tools-drawer__toolbar">
      <el-button type="primary" :loading="discovering" @click="discover">
        发现工具
      </el-button>
    </div>
    <!-- 交互提示：清单是入库快照不是实时连接；重复发现 id 稳定（upsert 锚点） -->
    <p class="tools-drawer__hint">
      清单为最近一次「发现工具」的入库结果；重复发现幂等，工具 id 稳定
    </p>

    <el-table v-loading="loading" :data="tools">
      <el-table-column prop="name" label="工具名" min-width="150">
        <template #default="{ row }">
          <span class="tool-name">{{ row.name }}</span>
        </template>
      </el-table-column>
      <el-table-column
        prop="description"
        label="说明"
        min-width="200"
        show-overflow-tooltip
      />
      <el-table-column label="操作" width="60" align="right">
        <template #default="{ row }">
          <!-- as 收窄：el-table 行是弱类型 DefaultRow，实际数据即 tools 里的 McpTool -->
          <el-button link type="primary" @click="openRun(row as McpTool)">
            运行
          </el-button>
        </template>
      </el-table-column>
      <template #empty>
        <el-empty description="暂无工具；点击「发现工具」从 server 拉取" :image-size="80" />
      </template>
    </el-table>

    <!-- 运行工具弹窗：append-to-body 脱离 drawer 的 overlay 层级 -->
    <el-dialog v-model="runVisible" title="运行工具" width="560px" append-to-body>
      <el-form label-width="80px">
        <el-form-item label="工具">
          <el-select v-model="runToolName" style="width: 100%">
            <el-option
              v-for="t in tools"
              :key="t.id"
              :label="t.name"
              :value="t.name"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="参数 JSON">
          <el-input
            v-model="runArgsText"
            type="textarea"
            :rows="4"
            placeholder='如 {"path":"/tmp/hello.txt"}；空 = 无参（须为 JSON 对象）'
          />
        </el-form-item>
        <el-form-item label="返回内容">
          <el-input
            v-model="runResult"
            type="textarea"
            :rows="4"
            readonly
            placeholder="运行后展示（多条 TextContent 换行拼接）"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="runVisible = false">关闭</el-button>
        <el-button type="primary" :loading="running" @click="run">运行</el-button>
      </template>
    </el-dialog>
  </el-drawer>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { notifyError, notifySuccess } from '@/utils/notify'
import {
  discoverMcpServer,
  getMcpServer,
  runMcpTool,
  type McpServerDetail,
  type McpTool,
} from '@/api/mcp'

/** 抽屉打开入参：server 行的最小子集（McpServerList 的 McpServerItem 结构化满足） */
export interface McpServerRef {
  id: string
  name: string
}

const visible = ref(false)
const current = ref<McpServerRef | null>(null)
const title = computed(() =>
  current.value ? `${current.value.name} · 工具清单` : '工具清单',
)

const loading = ref(false)
const tools = ref<McpTool[]>([])

/** 打开抽屉：换 server 后强制重拉（清单随 discover 入库变化） */
function open(server: McpServerRef): void {
  current.value = server
  visible.value = true
  void reload()
}

async function reload(): Promise<void> {
  const server = current.value
  if (!server) return
  loading.value = true
  try {
    const detail: McpServerDetail = await getMcpServer(server.id)
    tools.value = detail.tools
  } catch {
    // 拦截器已提示；空表即反馈
  } finally {
    loading.value = false
  }
}

// ---- 发现工具：连通 + tools/list + upsert（结果按 success 分支提示） ----

const discovering = ref(false)

async function discover(): Promise<void> {
  const server = current.value
  if (!server) return
  discovering.value = true
  try {
    const r = await discoverMcpServer(server.id)
    if (r.success) {
      notifySuccess(`发现成功 · ${r.latency_ms}ms · 共 ${r.tool_count} 个工具`)
    } else {
      notifyError(`连接失败：${r.error_message || '未知原因'}`)
    }
  } catch {
    // 已由拦截器提示
  } finally {
    discovering.value = false
    void reload() // 成功后清单已变；失败重拉亦无害
  }
}

// ---- 运行工具：实调（工具不在清单 → 404，已由拦截器提示） ----

const runVisible = ref(false)
const runToolName = ref('')
const runArgsText = ref('')
const runResult = ref('')
const running = ref(false)

/** 入参只用到工具名（el-table 行弱类型 DefaultRow 结构化满足） */
function openRun(tool: { name: string }): void {
  runToolName.value = tool.name
  runArgsText.value = ''
  runResult.value = ''
  runVisible.value = true
}

async function run(): Promise<void> {
  const server = current.value
  if (!server || runToolName.value === '') return
  // 参数 JSON：空 = 无参；非空须为 JSON 对象（对齐后端 Validate，本地提前提示）
  let args: Record<string, unknown> | undefined
  const text = runArgsText.value.trim()
  if (text !== '') {
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch {
      notifyError('参数不是合法 JSON')
      return
    }
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      notifyError('参数须为 JSON 对象，如 {"path":"/tmp/hello.txt"}')
      return
    }
    args = parsed as Record<string, unknown>
  }
  running.value = true
  try {
    const r = await runMcpTool(server.id, {
      tool_name: runToolName.value,
      arguments: args,
    })
    runResult.value = r.content
  } catch {
    // 结果区保持原值；错误提示已由拦截器弹
  } finally {
    running.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.tools-drawer__toolbar {
  display: flex;
  justify-content: flex-end;
  gap: var(--hf-space-2);
}

.tools-drawer__hint {
  margin: var(--hf-space-2) 0 var(--hf-space-4);
  font-size: var(--hf-font-size-sm);
  color: var(--hf-text-3);
}

/* 工具名等宽展示（区别于说明文本，对齐代码语义） */
.tool-name {
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-sm);
}
</style>
