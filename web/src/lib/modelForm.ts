import { fmtLabel, fmtNumber } from '@/base/format'
import { REASONING_EFFORT } from '@/base/labels'

export const EFFORT_PRESETS = ['low', 'medium', 'high', 'max'] as const

// Custom levels typed by the user fall through as-is.
export const effortLabel = (v: string) => fmtLabel(REASONING_EFFORT, v)

const EFFORT_RE = /^[a-zA-Z0-9_-]{1,32}$/

export function effortError(v: string): string {
  if (!v) return ''
  return EFFORT_RE.test(v) ? '' : '思考强度只能包含字母、数字、下划线和短横线，最多 32 个字符'
}

export interface EffortOption {
  value: string
  label: string
  hint?: string
}

// Options for the effort Combobox: the presets matching what was typed, plus the
// typed text itself so any level the provider accepts can be entered.
export function effortOptions(query: string): EffortOption[] {
  const q = query.trim()
  const presets: EffortOption[] = EFFORT_PRESETS.map((p) => ({ value: p, label: effortLabel(p), hint: p }))
  const shown = q ? presets.filter((o) => o.value.includes(q.toLowerCase()) || o.label.includes(q)) : presets
  if (q && !presets.some((o) => o.value === q || o.label === q)) shown.push({ value: q, label: q, hint: '自定义' })
  return shown
}

export const CONTEXT_PRESETS = [
  { value: 32_000, label: '32K' },
  { value: 128_000, label: '128K' },
  { value: 200_000, label: '200K' },
  { value: 256_000, label: '256K' },
  { value: 400_000, label: '400K' },
  { value: 512_000, label: '512K' },
  { value: 1_000_000, label: '1M' },
  { value: 2_000_000, label: '2M' },
] as const

export const DEFAULT_CONTEXT = 200_000

export function isContextPreset(v: number): boolean {
  return CONTEXT_PRESETS.some((p) => p.value === v)
}

// "1M", "256K", "300000" → tokens; 0 when not understood.
export function parseTokens(text: string): number {
  const m = /^\s*(\d+(?:\.\d+)?)\s*([kKmM万]?)\s*$/.exec(text)
  if (!m) return 0
  const n = Number(m[1])
  const unit = m[2].toLowerCase()
  const mult = unit === 'k' ? 1000 : unit === 'm' ? 1_000_000 : unit === '万' ? 10_000 : 1
  return Math.round(n * mult)
}

export function fmtTokens(n: number): string {
  if (!n) return '—'
  if (n % 1_000_000 === 0) return `${n / 1_000_000}M`
  if (n % 1000 === 0) return `${n / 1000}K`
  return fmtNumber(n)
}

export function contextError(v: number): string {
  if (!Number.isInteger(v) || v <= 0) return '请输入上下文长度，例如 300K'
  if (v < 4000) return '上下文长度至少 4K'
  return ''
}

export function baseUrlError(v: string): string {
  if (!v.trim()) return ''
  try {
    const u = new URL(v.trim())
    return u.protocol === 'http:' || u.protocol === 'https:' ? '' : '地址需以 http:// 或 https:// 开头'
  } catch {
    return '地址格式不正确'
  }
}

export type ProviderKind = 'openai' | 'claude' | 'ollama'

export interface ModelPreset {
  id: string
  title: string
  desc: string
  provider: ProviderKind
  baseUrl: string
  // where to get a key; empty when none is needed
  keyUrl: string
  keyRequired: boolean
  // suggested when the provider cannot list its models; first is the default
  models: string[]
  // context for models this table does not know
  context: number
  temperature: number
}

// Defaults follow what each provider documents; anything listed by the
// provider itself takes precedence in the picker.
export const MODEL_PRESETS: ModelPreset[] = [
  {
    id: 'deepseek',
    title: 'DeepSeek',
    desc: '国内直连，价格低',
    provider: 'openai',
    baseUrl: 'https://api.deepseek.com/v1',
    keyUrl: 'https://platform.deepseek.com/api_keys',
    keyRequired: true,
    models: ['deepseek-flash', 'deepseek-chat', 'deepseek-reasoner'],
    context: 1_000_000,
    temperature: 0.3,
  },
  {
    id: 'dashscope',
    title: '通义千问',
    desc: '阿里云百炼，有能读图的模型',
    provider: 'openai',
    baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    keyUrl: 'https://bailian.console.aliyun.com/?apiKey=1',
    keyRequired: true,
    models: ['qwen-plus', 'qwen-max', 'qwen-vl-max'],
    context: 128_000,
    temperature: 0.3,
  },
  {
    id: 'moonshot',
    title: 'Kimi',
    desc: '月之暗面，长上下文',
    provider: 'openai',
    baseUrl: 'https://api.moonshot.cn/v1',
    keyUrl: 'https://platform.moonshot.cn/console/api-keys',
    keyRequired: true,
    models: ['kimi-k3'],
    context: 1_000_000,
    temperature: 0.3,
  },
  {
    id: 'zhipu',
    title: '智谱 GLM',
    desc: '智谱开放平台',
    provider: 'openai',
    baseUrl: 'https://open.bigmodel.cn/api/paas/v4',
    keyUrl: 'https://open.bigmodel.cn/usercenter/apikeys',
    keyRequired: true,
    models: ['glm-4.7-flash'],
    context: 200_000,
    temperature: 0.3,
  },
  {
    id: 'openai',
    title: 'OpenAI',
    desc: 'GPT 系列，国内需要代理',
    provider: 'openai',
    baseUrl: 'https://api.openai.com/v1',
    keyUrl: 'https://platform.openai.com/api-keys',
    keyRequired: true,
    models: ['gpt-5', 'gpt-5-mini'],
    context: 400_000,
    temperature: 0.3,
  },
  {
    id: 'claude',
    title: 'Claude',
    desc: 'Anthropic，国内需要代理',
    provider: 'claude',
    baseUrl: 'https://api.anthropic.com',
    keyUrl: 'https://console.anthropic.com/settings/keys',
    keyRequired: true,
    models: ['claude-sonnet-5-5', 'claude-opus-5-5', 'claude-haiku-5-5'],
    context: 1_000_000,
    // current Claude models reject a temperature; 0 sends none
    temperature: 0,
  },
  {
    id: 'ollama',
    title: 'Ollama',
    desc: '在自己电脑上运行，不用密钥',
    provider: 'ollama',
    baseUrl: 'http://127.0.0.1:11434/v1',
    keyUrl: '',
    keyRequired: false,
    models: ['qwen2.5:7b'],
    context: 32_000,
    temperature: 0.3,
  },
  {
    id: 'custom',
    title: '其他（OpenAI 兼容）',
    desc: '硅基流动、OpenRouter、自建 vLLM 等',
    provider: 'openai',
    baseUrl: '',
    keyUrl: '',
    keyRequired: false,
    models: [],
    context: 128_000,
    temperature: 0.3,
  },
]

const trimUrl = (u: string) => u.trim().replace(/\/+$/, '')

// Which preset an existing configuration came from; 'custom' when none matches.
export function presetFor(provider: ProviderKind, baseUrl: string): ModelPreset {
  const url = trimUrl(baseUrl)
  const byId = (id: string) => MODEL_PRESETS.find((p) => p.id === id)!
  // an empty address means the provider's own default endpoint
  if (!url) return byId(provider)
  return MODEL_PRESETS.find((p) => p.provider === provider && p.baseUrl && trimUrl(p.baseUrl) === url) ?? byId('custom')
}

export interface ModelCaps {
  context: number
  vision: boolean
  thinking: boolean
}

const CONTEXT_RULES: [RegExp, number][] = [
  [/^claude-haiku-4/, 200_000],
  [/^claude-/, 1_000_000],
  [/gpt-4\.1/, 1_000_000],
  [/gpt-5/, 400_000],
  [/gpt-4o|o\d(-mini)?$/, 128_000],
  [/deepseek/, 1_000_000],
  [/kimi|moonshot/, 1_000_000],
  [/glm/, 200_000],
  [/qwen-long/, 1_000_000],
  [/qwen/, 128_000],
  [/gemini/, 1_000_000],
  [/llama|mistral|gemma|phi/, 128_000],
]

const VISION = /vision|vl\b|-vl|4o|gpt-4\.1|gpt-5|claude|gemini|glm-\d+(\.\d+)?v|llava|minicpm-v|pixtral|kimi-k3/
const THINKING = /reasoner|thinking|\br1\b|-r1|qwq/

// Best guess from the model name; the user can still change every value.
export function guessCaps(model: string, preset: ModelPreset): ModelCaps {
  const m = model.toLowerCase()
  const rule = CONTEXT_RULES.find(([re]) => re.test(m))
  // local models run with a small window unless the user raises it
  const context = preset.provider === 'ollama' ? preset.context : (rule?.[1] ?? preset.context)
  return { context, vision: VISION.test(m), thinking: THINKING.test(m) }
}

// What the provider receives when 单次最多输出 is left empty.
export const maxTokensDefault = (provider: ProviderKind) => (provider === 'claude' ? '8192' : '不限制，由服务商决定')
