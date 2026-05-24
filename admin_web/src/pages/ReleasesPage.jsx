import { useState, useEffect, useRef } from 'react'
import { Rocket } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchReleases, uploadRelease, activateRelease } from '../api/admin.js'
import CrudDialog, { FormField, TextInput, TextArea, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import StatusBadge from '../components/StatusBadge.jsx'
import {
  Button,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  TableSkeleton,
  EmptyState,
} from '../components/ui/index.js'

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

  const headerActions = can('releases.write') ? (
    <Button variant="primary" size="md" onClick={openUpload}>+ {t.uploadRelease}</Button>
  ) : null

  return (
    <div>
      <PageHeader title={t.releasesTitle} actions={headerActions} />

      {error && <p className="text-danger text-sm mb-4">{error}</p>}

      {loading ? (
        <TableSkeleton rows={8} cols={can('releases.write') ? 7 : 6} />
      ) : releases.length === 0 ? (
        <EmptyState
          icon={<Rocket className="w-12 h-12" />}
          title={t.noReleases}
          description="Загрузите первый релиз приложения"
          action={can('releases.write') ? <Button variant="primary" onClick={openUpload}>+ {t.uploadRelease}</Button> : null}
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th>ID</Th>
              <Th>{t.fieldPlatform}</Th>
              <Th>{t.fieldVersion}</Th>
              <Th>{t.fieldMandatory}</Th>
              <Th>{t.fieldStatus}</Th>
              <Th>{t.fieldReleaseNotes}</Th>
              {can('releases.write') && <Th></Th>}
            </Tr>
          </Thead>
          <Tbody>
            {releases.map(r => (
              <Tr key={r.id}>
                <Td mono>{r.id}</Td>
                <Td>{r.platform}</Td>
                <Td mono>{r.version}</Td>
                <Td>{r.mandatory ? '✓' : ''}</Td>
                <Td><StatusBadge status={r.is_active ? 'Active' : 'Inactive'} /></Td>
                <Td className="text-tertiary truncate max-w-[200px]">{r.release_notes || '—'}</Td>
                {can('releases.write') && (
                  <Td>
                    {!r.is_active && (
                      <Button variant="ghost" size="sm" onClick={() => doActivate(r.id)}>{t.activate}</Button>
                    )}
                  </Td>
                )}
              </Tr>
            ))}
          </Tbody>
        </Table>
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
            className="text-sm text-tertiary file:mr-3 file:py-1.5 file:px-3 file:rounded file:border-0 file:bg-accent file:text-white file:text-sm file:cursor-pointer hover:file:bg-accent-hover"
          />
          {file && <p className="text-xs text-tertiary mt-1">{t.fieldFileSelected}: {file.name}</p>}
        </FormField>
      </CrudDialog>
    </div>
  )
}
