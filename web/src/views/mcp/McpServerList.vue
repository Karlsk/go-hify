<template>
  <!-- MCP Server 列表页：HifyTable + HifyFormDialog + useConfirm + 真实 API（api/mcp.ts）。
       工具数是列表聚合列（后端分页窗口批量计数）；发现 = 连通 + 工具发现 + 入库一个动作，
       结果按 success 分支提示；编辑需 env 回填，先取详情再开弹窗。 -->
  <div>
    <PageHeader
      title="MCP 服务管理"
      description="统一接入 MCP 工具服务：添加 server、发现工具、实调验证"
    >
      <template #actions>
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon>
          新增 MCP 服务
        </el-button>
      </template>
    </PageHeader>

    <HifyTable ref="tableRef" :columns="columns" :api="getMcpServerList">
      <template #transport="{ row }">
        <el-tag :type="TRANSPORT_TAG[row.transport]" size="small">
          {{ TRANSPORT_LABEL[row.transport] }}
        </el-tag>
      </template>
      <template #endpoint="{ row }">
        <!-- stdio 显示启动命令、http/sse 显示端点地址（次级文本，超长省略） -->
        <span class="endpoint">{{ row.transport === 'stdio' ? row.command : row.url }}</span>
      </template>
      <template #toolCount="{ row }">
        <!-- 点击工具数展开该 server 的工具抽屉（唯一入口） -->
        <el-button link type="primary" @click="toolsRef?.open(row)">
          {{ row.tool_count }}
        </el-button>
      </template>
      <template #status="{ row }">
        <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
          {{ row.enabled ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #createdAt="{ row }">
        {{ formatDateTime(row.created_at) }}
      </template>
      <template #actions="{ row }">
        <el-button
          link
          type="primary"
          :loading="discoveringId === row.id"
          @click="discover(row)"
        >
          发现
        </el-button>
        <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
        <el-button link type="danger" @click="remove(row)">删除</el-button>
      </template>
    </HifyTable>

    <!-- 工具子资源抽屉：open(server) 结构化满足 McpServerRef -->
    <McpToolsDrawer ref="toolsRef" />

    <HifyFormDialog
      ref="dialogRef"
      v-model="dialogVisible"
      title="MCP 服务"
      :initial-model="emptyForm"
      :rules="rules"
      @submit="onSubmit"
    >
      <template #default="{ form }">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="如 filesystem / 内部工具网关" />
        </el-form-item>
        <el-form-item label="传输方式" prop="transport">
          <el-select
            v-model="form.transport"
            style="width: 100%"
            @change="onTransportChange"
          >
            <el-option
              v-for="t in TRANSPORT_OPTIONS"
              :key="t.value"
              :label="t.label"
              :value="t.value"
            />
          </el-select>
        </el-form-item>
        <!-- 必填按 transport 分支（对齐后端 Validate）：stdio 命令、http/sse 端点 -->
        <el-form-item v-if="form.transport === 'stdio'" label="启动命令" prop="command">
          <el-input
            v-model="form.command"
            placeholder="如 npx -y @modelcontextprotocol/server-filesystem /tmp"
          />
        </el-form-item>
        <el-form-item v-else label="端点地址" prop="url">
          <el-input v-model="form.url" placeholder="如 http://127.0.0.1:8001/mcp" />
        </el-form-item>
        <el-form-item v-if="form.transport === 'stdio'" label="环境变量" prop="envText">
          <el-input
            v-model="form.envText"
            type="textarea"
            :rows="3"
            placeholder='JSON 对象，如 {"NODE_ENV":"production"}；空 = 无'
          />
        </el-form-item>
        <!-- PUT 全量提交含 enabled：不处理的话任何编辑都会把 server 停用（后端缺省 true） -->
        <el-form-item v-if="isEdit" label="启用" prop="enabled">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </template>
    </HifyFormDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { FormRules } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import HifyTable, { type HifyTableColumn } from '@/components/HifyTable.vue'
import HifyFormDialog from '@/components/HifyFormDialog.vue'
import McpToolsDrawer from '@/components/McpToolsDrawer.vue'
import { BREAKPOINTS } from '@/composables/useBreakpoint'
import { useConfirm } from '@/composables/useConfirm'
import { notifyError, notifySuccess } from '@/utils/notify'
import {
  createMcpServer,
  deleteMcpServer,
  discoverMcpServer,
  getMcpServer,
  getMcpServerList,
  updateMcpServer,
  type McpServerItem,
  type McpServerSaveData,
  type McpTransport,
} from '@/api/mcp'

// ---- transport 展示与校验配置（对齐后端 api 层枚举） ----

const TRANSPORT_OPTIONS: Array<{ value: McpTransport; label: string }> = [
  { value: 'stdio', label: '本地进程（stdio）' },
  { value: 'http', label: '远程 HTTP' },
  { value: 'sse', label: '远程 SSE' },
]

// EP 仅 5 种 tag 色
const TRANSPORT_TAG: Record<McpTransport, 'primary' | 'success' | 'warning'> = {
  stdio: 'primary',
  http: 'success',
  sse: 'warning',
}

const TRANSPORT_LABEL: Record<McpTransport, string> = {
  stdio: 'stdio 本地',
  http: 'HTTP',
  sse: 'SSE',
}

// ---- 表格 ----

const tableRef = ref<{ refresh: () => void }>()
const columns: HifyTableColumn[] = [
  { label: '名称', prop: 'name' },
  { label: '传输', slot: 'transport', width: 100 },
  { label: '端点 / 命令', slot: 'endpoint' },
  { label: '工具数', slot: 'toolCount', width: 70 },
  { label: '状态', slot: 'status', width: 80 },
  { label: '创建时间', slot: 'createdAt', width: 150, hideBelow: BREAKPOINTS.md },
  { label: '操作', slot: 'actions', width: 160, align: 'right' },
]

// ---- 工具子资源抽屉 ----

const toolsRef = ref<{ open: (server: { id: string; name: string }) => void }>()

// ---- 发现工具（连通上限 10s：per-row loading，结束刷新工具数列） ----

const discoveringId = ref<string | null>(null)

async function discover(row: McpServerItem): Promise<void> {
  discoveringId.value = row.id
  try {
    const r = await discoverMcpServer(row.id)
    // 连通失败是业务结果（HTTP 200 + success:false），显式提示；HTTP 层失败由拦截器弹
    if (r.success) {
      notifySuccess(`发现成功 · ${r.latency_ms}ms · 共 ${r.tool_count} 个工具`)
    } else {
      notifyError(`连接失败：${r.error_message || '未知原因'}`)
    }
  } catch {
    // 已由拦截器提示
  } finally {
    discoveringId.value = null
    tableRef.value?.refresh() // 成功后 tool_count 已变
  }
}

// ---- 新增 / 编辑弹窗 ----

/** 表单模型：id='' 为新增；envText 是 JSON 文本（编辑回填详情 env，提交时解析） */
interface McpServerForm {
  id: string
  name: string
  transport: McpTransport
  command: string
  url: string
  envText: string
  enabled: boolean
}

const dialogRef = ref<{ open: (data?: McpServerForm) => void }>()
const dialogVisible = ref(false)
const isEdit = ref(false)
/** rules 需感知弹窗内的 transport（必填按传输方式分支）：选择变化时同步镜像 */
const formTransport = ref<McpTransport>('stdio')

const emptyForm = (): McpServerForm => ({
  id: '',
  name: '',
  transport: 'stdio',
  command: '',
  url: '',
  envText: '',
  enabled: true,
})

function onTransportChange(transport: McpTransport): void {
  formTransport.value = transport
}

/** env 非空时校验 JSON 对象且值全为字符串（对齐后端 map[string]string 绑定） */
function validateEnv(
  _rule: unknown,
  value: string,
  callback: (error?: Error) => void,
): void {
  const text = value.trim()
  if (text === '') {
    callback()
    return
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    callback(new Error('env 不是合法 JSON'))
    return
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    callback(new Error('env 须为 JSON 对象，如 {"NODE_ENV":"production"}'))
    return
  }
  if (Object.values(parsed as Record<string, unknown>).some((v) => typeof v !== 'string')) {
    callback(new Error('env 的值须全为字符串'))
    return
  }
  callback()
}

const rules = computed<FormRules>(() => ({
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  transport: [{ required: true, message: '请选择传输方式', trigger: 'change' }],
  command:
    formTransport.value === 'stdio'
      ? [{ required: true, message: 'stdio 模式必须提供启动命令', trigger: 'blur' }]
      : [],
  url:
    formTransport.value === 'stdio'
      ? []
      : [{ required: true, message: '远程模式必须提供端点地址', trigger: 'blur' }],
  envText: [{ validator: validateEnv, trigger: 'blur' }],
}))

function openCreate(): void {
  isEdit.value = false
  formTransport.value = 'stdio'
  dialogRef.value?.open()
}

/** 编辑：列表行无 env，先取详情回填（envText 为格式化 JSON，空对象回空串） */
function openEdit(row: McpServerItem): void {
  isEdit.value = true
  formTransport.value = row.transport
  void getMcpServer(row.id)
    .then((d) => {
      dialogRef.value?.open({
        id: d.id,
        name: d.name,
        transport: d.transport,
        command: d.command,
        url: d.url,
        envText: Object.keys(d.env).length > 0 ? JSON.stringify(d.env, null, 2) : '',
        enabled: d.enabled,
      })
    })
    .catch(() => {
      // 拦截器已提示；弹窗不打开
    })
}

/** 解析 envText → env map（空文本 = 空 map）；校验已由表单规则把关 */
function parseEnv(text: string): Record<string, string> {
  const trimmed = text.trim()
  if (trimmed === '') return {}
  return JSON.parse(trimmed) as Record<string, string>
}

function onSubmit(form: McpServerForm, done: (ok?: boolean) => void): void {
  // env 仅 stdio 提交（其他 transport 后端存而不用，省键）；空 JSON = 清空 env
  const payload: McpServerSaveData = {
    name: form.name,
    transport: form.transport,
    command: form.command.trim(),
    url: form.url.trim(),
    env: form.transport === 'stdio' ? parseEnv(form.envText) : undefined,
    enabled: form.enabled,
  }
  if (form.id === '') {
    void createMcpServer(payload)
      .then(() => {
        notifySuccess('创建成功')
        done(true)
        tableRef.value?.refresh()
      })
      .catch(() => done(false)) // 失败提示已由拦截器弹；保持弹窗打开
  } else {
    // PUT 全量提交：必须带 enabled（漏发会被后端置回启用）
    void updateMcpServer(form.id, payload)
      .then(() => {
        notifySuccess('更新成功')
        done(true)
        tableRef.value?.refresh()
      })
      .catch(() => done(false))
  }
}

// ---- 删除：useConfirm 一行全流程（被 Agent 绑定工具时 409 由拦截器提示） ----

function remove(row: McpServerItem): void {
  void useConfirm({
    message: `删除 MCP 服务「${row.name}」？已入库的工具将一并删除；有 Agent 绑定其工具时无法删除（可先解绑或改为停用）。`,
    api: () => deleteMcpServer(row.id),
  }).then((ok) => {
    if (ok) tableRef.value?.refresh()
  })
}

// ---- 时间展示：RFC 3339 → YYYY-MM-DD HH:mm（本地时区） ----

function formatDateTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number): string => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(
    d.getHours(),
  )}:${pad(d.getMinutes())}`
}
</script>

<style scoped>
/* 端点 / 命令列：次级文本 + 超长省略（命令常带长参数） */
.endpoint {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--hf-text-3);
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-sm);
}
</style>
