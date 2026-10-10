/**
 * 工作流创建草稿 store（Pinia，spec 010 US4）：两步式创建的第一步 ↔ 第二步
 * 数据传递（第一步表单 / 第二步图编排互访保留，FR-014）。
 *
 * 生命周期 = 内存态：刷新 / 直访第二步时 state 归零 → hasForm false → 第二步
 * 挂载守卫回第一步（「直访 / 刷新回第一步」语义天然成立，不落 localStorage——
 * 内部工具，创建草稿不值得持久化，research #1 / data-model §3）。
 */
import { defineStore } from 'pinia'
import type { SchemaField, WorkflowType } from '@/api/workflow'
import type { GraphConfig } from '@/views/workflow/graph'

/** 第一步表单快照（saveForm 整体写入）。outputSchema 不在第一步编辑（spec 018
 *  FR-005 入口收敛：出参唯一入口 = 第二步画布 end 节点「输出映射」，经
 *  saveOutputSchema 写回——saveForm 不得覆盖它） */
export interface WorkflowFormDraft {
  name: string
  description: string
  type: WorkflowType
  inputSchema: SchemaField[]
}

export const useWorkflowCreateDraftStore = defineStore('workflowCreateDraft', {
  state: () => ({
    name: '',
    description: '',
    type: 'chat' as WorkflowType,
    inputSchema: [] as SchemaField[],
    /** task 型出参（spec 018：由第二步画布 end 映射推导写回，第一步不再编辑） */
    outputSchema: [] as SchemaField[],
    /** 第二步图编排（null = 尚未进过第二步，初始用 PREFILL 深拷贝） */
    graph: null as GraphConfig | null,
  }),
  getters: {
    /** 第一步已填过（name 非空）：false → 第二步挂载守卫回第一步 */
    hasForm: (state) => state.name !== '',
  },
  actions: {
    /** 第一步「创建工作流」：表单整体写入（outputSchema 不动——end 映射产出） */
    saveForm(form: WorkflowFormDraft) {
      this.name = form.name
      this.description = form.description
      this.type = form.type
      this.inputSchema = form.inputSchema
    },
    /** 画布开始节点面板编辑写回（FR-002 单源：与第一步表单同一字段，编辑即写回） */
    saveInputSchema(rows: SchemaField[]) {
      this.inputSchema = rows
    },
    /** 画布 end 节点「输出映射」推导 schema 写回（spec 018 FR-004/FR-005） */
    saveOutputSchema(rows: SchemaField[]) {
      this.outputSchema = rows
    },
    /** 第二步离开（上一步 / 侧边栏误点）：图编排回写 */
    saveGraph(graph: GraphConfig) {
      this.graph = graph
    },
    /** 创建成功：草稿清零 */
    clear() {
      this.name = ''
      this.description = ''
      this.type = 'chat'
      this.inputSchema = []
      this.outputSchema = []
      this.graph = null
    },
  },
})
