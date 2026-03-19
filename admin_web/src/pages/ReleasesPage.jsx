import { useState, useEffect, useRef } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchReleases, uploadRelease, activateRelease } from '../api/admin.js'
import CrudDialog, { FormField, TextInput, TextArea, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import StatusBadge from '../components/StatusBadge.jsx'

export default function ReleasesPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [releases, setReleases] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [dialog, setDialog] = useState(false)
  const [saving, setSaving] = useState(false)

  const [platform, setPlatform] = useState('android')
  const [version, setVersion] = useState('')
  const [releaseNotes, setReleaseNotes] = useState('')
  const [mandatory, setMandatory] = useState(false)
  const [minSupportedVersion, setMinSupportedVersion] = useState('')
  const [activate, setActivate] = useState(true)
  const [file, setFile] = useState(null)
  const fileRef = useRef()

  async function load() {
    setLoading(true)
    try {
      const data = await fetchReleases()
      setReleases(data.releases || data || [])
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  function openUpload() {
    setPlatform('android'); setVersion(''); setReleaseNotes('')
    setMandatory(false); setMinSupportedVersion(''); setActivate(true); setFile(null)
    setDialog(true)
  }

  async function handleUpload() {
    if (!file) { toast.error('Select a file'); return }
    setSaving(true)
    try {
      await uploadRelease({ platform, version, releaseNotes, mandatory, minSupportedVersion, file, activate })
      toast.success(t.upload + ' OK')
      setDialog(false)
      load()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function doActivate(id) {
    try {
      await activateRelease(id)
      toast.success(t.activate + ' OK')
      load()
    } catch (err) {
      toast.error(err.message)
    }
  }

  if (loading) return <p className="text-text-secondary">{t.loading}</p>
  if (error) return <p className="text-red-400">{error}</p>

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-2xl font-bold">{t.releasesTitle}</h2>
        {can('releases.write') && (
          <button onClick={openUpload} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.uploadRelease}
          </button>
        )}
      </div>

      {releases.length === 0 ? (
        <p className="text-text-secondary">{t.noReleases}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">{t.fieldPlatform}</th>
                <th className="py-2 px-3">{t.fieldVersion}</th>
                <th className="py-2 px-3">{t.fieldMandatory}</th>
                <th className="py-2 px-3">{t.fieldStatus}</th>
                <th className="py-2 px-3">{t.fieldReleaseNotes}</th>
                {can('releases.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {releases.map(r => (
                <tr key={r.id} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3">{r.id}</td>
                  <td className="py-2 px-3">{r.platform}</td>
                  <td className="py-2 px-3 font-mono">{r.version}</td>
                  <td className="py-2 px-3">{r.mandatory ? '\u2713' : ''}</td>
                  <td className="py-2 px-3"><StatusBadge status={r.is_active ? 'Active' : 'Inactive'} /></td>
                  <td className="py-2 px-3 text-text-secondary truncate max-w-[200px]">{r.release_notes || '\u2014'}</td>
                  {can('releases.write') && (
                    <td className="py-2 px-3">
                      {!r.is_active && (
                        <button onClick={() => doActivate(r.id)} className="text-brand hover:underline text-xs">{t.activate}</button>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <CrudDialog
        open={dialog} title={t.uploadRelease}
        onClose={() => setDialog(false)} onSubmit={handleUpload} loading={saving}
      >
        <FormField label={t.fieldPlatform}>
          <SelectInput value={platform} onChange={setPlatform} options={[
            { value: 'android', label: 'Android' },
            { value: 'ios', label: 'iOS' },
            { value: 'windows', label: 'Windows' },
            { value: 'linux', label: 'Linux' },
            { value: 'macos', label: 'macOS' },
          ]} />
        </FormField>
        <FormField label={t.fieldVersion}><TextInput value={version} onChange={setVersion} placeholder="1.0.0" /></FormField>
        <FormField label={t.fieldReleaseNotes}><TextArea value={releaseNotes} onChange={setReleaseNotes} rows={2} /></FormField>
        <FormField label={t.fieldMinSupportedVersion}><TextInput value={minSupportedVersion} onChange={setMinSupportedVersion} /></FormField>
        <Checkbox label={t.fieldMandatory} checked={mandatory} onChange={setMandatory} />
        <Checkbox label={t.activate} checked={activate} onChange={setActivate} />
        <FormField label={t.fieldApkFile}>
          <input
            ref={fileRef}
            type="file"
            onChange={(e) => setFile(e.target.files[0] || null)}
            className="text-sm text-text-secondary file:mr-3 file:py-1.5 file:px-3 file:rounded-lg file:border-0 file:bg-brand file:text-white file:text-sm file:cursor-pointer hover:file:bg-brand-dark"
          />
          {file && <p className="text-xs text-text-secondary mt-1">{t.fieldFileSelected}: {file.name}</p>}
        </FormField>
      </CrudDialog>
    </div>
  )
}
