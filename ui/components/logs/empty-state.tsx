'use client'

import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getExampleBaseUrl } from '@/lib/utils/port'
import { AlertTriangle, Copy } from 'lucide-react'
import { useMemo, useState } from 'react'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '../ui/alert'
import { CodeEditor } from './ui/code-editor'

type Provider = 'openai' | 'anthropic' | 'genai' | 'litellm'
type Language = 'python' | 'typescript'

type Examples = {
  curl: string
  sdk: {
    [P in Provider]: {
      [L in Language]: string
    }
  }
}

// Common editor options to reduce duplication
const EDITOR_OPTIONS = {
  scrollBeyondLastLine: false,
  minimap: { enabled: false },
  lineNumbers: 'off',
  folding: false,
  lineDecorationsWidth: 0,
  lineNumbersMinChars: 0,
  glyphMargin: false,
} as const

interface CodeBlockProps {
  code: string
  language: string
  onLanguageChange?: (language: string) => void
  showLanguageSelect?: boolean
  readonly?: boolean
}

function CodeBlock({ code, language, onLanguageChange, showLanguageSelect = false, readonly = true }: CodeBlockProps) {
  const copyToClipboard = () => {
    navigator.clipboard.writeText(code)
    toast.success('Copied to clipboard')
  }

  return (
    <div className="relative">
      <div className="absolute right-4 top-4 z-10 flex items-center gap-2">
        {showLanguageSelect && onLanguageChange && (
          <Select value={language} onValueChange={onLanguageChange}>
            <SelectTrigger className="h-8 w-fit text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem className="text-xs" value="python">
                Python
              </SelectItem>
              <SelectItem className="text-xs" value="typescript">
                TypeScript
              </SelectItem>
            </SelectContent>
          </Select>
        )}
        <Button variant="ghost" size="icon" onClick={copyToClipboard}>
          <Copy className="size-4" />
        </Button>
      </div>
      <CodeEditor className="w-full" code={code} lang={language} readonly={readonly} height={300} fontSize={14} options={EDITOR_OPTIONS} />
    </div>
  )
}

interface EmptyStateProps {
  isSocketConnected: boolean
  error: string | null
}

export function EmptyState({ isSocketConnected, error }: EmptyStateProps) {
  const [language, setLanguage] = useState<Language>('python')

  // Generate examples dynamically using the port utility
  const examples: Examples = useMemo(() => {
    const baseUrl = getExampleBaseUrl()

    return {
      curl: `curl -X POST ${baseUrl}/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "openai/gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'`,
      sdk: {
        openai: {
          python: `import openai

client = openai.OpenAI(
    base_url="${baseUrl}/openai",
    api_key="dummy-api-key" # Handled by Bifrost
)

response = client.chat.completions.create(
    model="gpt-4o-mini", # or "provider/model" for other providers (anthropic/claude-3-sonnet)
    messages=[{"role": "user", "content": "Hello!"}]
)`,
          typescript: `import OpenAI from "openai";

const openai = new OpenAI({
  baseURL: "${baseUrl}/openai",
  apiKey: "dummy-api-key", // Handled by Bifrost
});

const response = await openai.chat.completions.create({
  model: "gpt-4o-mini", // or "provider/model" for other providers (anthropic/claude-3-sonnet)
  messages: [{ role: "user", content: "Hello!" }],
});`,
        },
        anthropic: {
          python: `import anthropic

client = anthropic.Anthropic(
    base_url="${baseUrl}/anthropic",
    api_key="dummy-api-key" # Handled by Bifrost
)

response = client.messages.create(
    model="claude-3-sonnet-20240229", # or "provider/model" for other providers (openai/gpt-4o-mini)
    max_tokens=1000,
    messages=[{"role": "user", "content": "Hello!"}]
)`,
          typescript: `import Anthropic from "@anthropic-ai/sdk";

const anthropic = new Anthropic({
  baseURL: "${baseUrl}/anthropic",
  apiKey: "dummy-api-key", // Handled by Bifrost
});

const response = await anthropic.messages.create({
  model: "claude-3-sonnet-20240229", // or "provider/model" for other providers (openai/gpt-4o-mini)
  max_tokens: 1000,
  messages: [{ role: "user", content: "Hello!" }],
});`,
        },
        genai: {
          python: `from google import genai
from google.genai.types import HttpOptions

client = genai.Client(
    api_key="dummy-api-key", # Handled by Bifrost
    http_options=HttpOptions(base_url="${baseUrl}/genai")
)

response = client.models.generate_content(
    model="gemini-2.5-pro", # or "provider/model" for other providers (openai/gpt-4o-mini)
    contents="Hello!"
)`,
          typescript: `import { GoogleGenerativeAI } from "@google/generative-ai";

const genAI = new GoogleGenerativeAI("dummy-api-key", { // Handled by Bifrost
  baseUrl: "${baseUrl}/genai",
});

const model = genAI.getGenerativeModel({ model: "gemini-2.5-pro" }); // or "provider/model" for other providers (openai/gpt-4o-mini)
const response = await model.generateContent("Hello!");`,
        },
        litellm: {
          python: `import litellm

litellm.api_base = "${baseUrl}/litellm"

response = litellm.completion(
    model="openai/gpt-4o-mini",
    messages=[{"role": "user", "content": "Hello!"}]
)`,
          typescript: `import { completion } from "litellm";

const response = await completion({
  model: "openai/gpt-4o-mini",
  messages: [{ role: "user", content: "Hello!" }],
  api_base: "${baseUrl}/litellm",
});`,
        },
      },
    }
  }, [])

  return (
    <div className="flex w-full flex-col items-center justify-center space-y-8">
      {error && (
        <Alert>
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="w-full space-y-6">
        <div className="flex flex-row items-center gap-2">
          <div>
            <h3 className="text-lg font-semibold">Integration under 60 seconds</h3>
            <p className="text-muted-foreground text-sm">Send your first request to get started</p>
          </div>
          <div className="ml-auto">
            {isSocketConnected && (
              <div className="inline-flex items-center rounded-full border border-green-200 bg-green-50 px-3 py-1 text-xs font-medium text-green-700 sm:px-4 sm:text-sm">
                <span className="relative mr-2 flex h-2 w-2 sm:mr-3">
                  <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-500 opacity-75"></span>
                  <span className="relative inline-flex h-2 w-2 rounded-full bg-green-600"></span>
                </span>
                <span>Listening for logs...</span>
              </div>
            )}
          </div>
        </div>

        <Tabs defaultValue="curl" className="w-full rounded-lg border">
          <TabsList className="grid h-10 w-full grid-cols-5 rounded-b-none rounded-t-lg">
            <TabsTrigger value="curl">cURL</TabsTrigger>
            <TabsTrigger value="openai">OpenAI SDK</TabsTrigger>
            <TabsTrigger value="anthropic">Anthropic SDK</TabsTrigger>
            <TabsTrigger value="genai">Google GenAI SDK</TabsTrigger>
            <TabsTrigger value="litellm">LiteLLM SDK</TabsTrigger>
          </TabsList>

          <TabsContent value="curl" className="px-4">
            <CodeBlock code={examples.curl} language="bash" readonly={false} />
          </TabsContent>

          <TabsContent value="openai" className="px-4">
            <CodeBlock
              code={examples.sdk.openai[language]}
              language={language}
              onLanguageChange={(newLang) => setLanguage(newLang as Language)}
              showLanguageSelect
            />
          </TabsContent>

          <TabsContent value="anthropic" className="px-4">
            <CodeBlock
              code={examples.sdk.anthropic[language]}
              language={language}
              onLanguageChange={(newLang) => setLanguage(newLang as Language)}
              showLanguageSelect
            />
          </TabsContent>

          <TabsContent value="genai" className="px-4">
            <CodeBlock
              code={examples.sdk.genai[language]}
              language={language}
              onLanguageChange={(newLang) => setLanguage(newLang as Language)}
              showLanguageSelect
            />
          </TabsContent>

          <TabsContent value="litellm" className="px-4">
            <CodeBlock
              code={examples.sdk.litellm[language]}
              language={language}
              onLanguageChange={(newLang) => setLanguage(newLang as Language)}
              showLanguageSelect
            />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}
