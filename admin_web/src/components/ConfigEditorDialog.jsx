import { useState, useEffect } from 'react'
import { fetchFileContent, saveFileContent } from '../api/admin'

const CONFIG_PATHS = [
  { label: 'Xray config', path: '/opt/heyearth/xray/config.json' },
  { label: 'Hysteria2 server', path: '/opt/heyearth/hysteria/config.yaml' },
  { label: 'Node-agent config', path: '/opt/heyearth/node-agent/config.yaml' },
  { label: 'Reality keys', path: '/opt/heyearth/state/reality-keys.json' },
]

export default function ConfigEditorDialog({ serverId, serverName, onClose }) {
  const [selectedPath, setSelectedPath] = useState(CONFIG_PATHS[0].path)
  const [content, setContent] = useState('')
  const [originalContent, setOriginalContent] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    loadFile(selectedPath)
  }, [selectedPath, serverId])

  async function loadFile(path) {
    setLoading(true)
    setError(null)
    setSaved(false)
    try {
      const data = await fetchFileContent(serverId, path)
      setContent(data.content || '')
      setOriginalContent(data.content || '')
    } catch (err) {
      setError(err.message)
      setContent('')
      setOriginalContent('')
    } finally {
      setLoading(false)
    }
  }

  async function handleSave() {
    setSaving(true)
    setError(null)
    try {
      await saveFileContent(serverId, selectedPath, content)
      setOriginalContent(content)
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    } catch (err) {
      setError(err.message)
    } finally {
      setSaving(false)
    }
  }

  const isDirty = content !== originalContent

  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4" onClick={onClose}>
      <div className="bg-surface-card rounded-xl border border-white/10 w-full max-w-4xl max-h-[90vh] flex flex-col"
           onClick={e => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
          <div>
            <h3 className="font-semibold">Config Editor — {serverName}</h3>
            <p className="text-xs text-text-secondary mt-0.5">{selectedPath}</p>
          </div>
          <button onClick={onClose} className="text-text-secondary hover:text-text-primary p-1">
            <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M4.646 4.646a.5.5 0 0 1 .708 0L8 7.293l2.646-2.647a.5.5 0 0 1 .708.708L8.707 8l2.647 2.646a.5.5 0 0 1-.708.708L8 8.707l-2.646 2.647a.5.5 0 0 1-.708-.708L7.293 8 4.646 5.354a.5.5 0 0 1 0-.708z"/></svg>
          </button>
        </div>

        {/* Tab bar for config files */}
        <div className="flex gap-1 px-4 py-2 border-b border-white/5 overflow-x-auto">
          {CONFIG_PATHS.map(cp => (
            <button key={cp.path}
              onClick={() => setSelectedPath(cp.path)}
              className={`text-xs px-3 py-1.5 rounded-lg whitespace-nowrap transition ${
                selectedPath === cp.path
                  ? 'bg-brand/20 text-brand'
                  : 'text-text-secondary hover:text-text-primary hover:bg-surface-alt'
              }`}>
              {cp.label}
            </button>
          ))}
        </div>

        {/* Editor */}
        <div className="flex-1 min-h-0 p-4">
          {loading ? (
            <div className="flex items-center justify-center h-full text-text-secondary">Loading...</div>
          ) : error ? (
            <div className="text-red-400 text-sm p-4 bg-red-500/10 rounded-lg">{error}</div>
          ) : (
            <textarea
              value={content}
              onChange={e => setContent(e.target.value)}
              className="w-full h-full min-h-[400px] bg-surface-bg rounded-lg border border-white/10 p-3 font-mono text-sm text-text-primary resize-none focus:outline-none focus:border-brand/50"
              spellCheck={false}
            />
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-4 py-3 border-t border-white/10">
          <div className="text-xs text-text-secondary">
            {isDirty && <span className="text-amber-400">Unsaved changes</span>}
            {saved && <span className="text-green-400">Saved!</span>}
          </div>
          <div className="flex gap-2">
            <button onClick={() => loadFile(selectedPath)}
              className="px-3 py-1.5 text-sm rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary transition">
              Reload
            </button>
            <button onClick={handleSave} disabled={!isDirty || saving}
              className="px-4 py-1.5 text-sm rounded-lg bg-brand text-white hover:bg-brand/80 disabled:opacity-40 disabled:cursor-not-allowed transition">
              {saving ? 'Saving...' : 'Save'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
