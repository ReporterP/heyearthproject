import { useState } from 'react'
import { UsersRound } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchGroupsPaged, createGroup, updateGroup, deleteGroup } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, Checkbox } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import {
  Badge,
  Button,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  EmptyState,
} from '../components/ui/index.js'

export default function GroupsPage() {
  // Deprecation banner
  return (
    <div>
      <div className="p-4 mb-6 rounded border border-warning/30 bg-warning/10 text-warning text-sm">
        <p className="font-semibold mb-1">Устаревшая страница</p>
        <p>Группы заменены на <a href="/plans" className="text-accent underline hover:text-accent-hover">Тарифные планы</a>. Управляйте подписками через страницу Планов. Группы сохранены для обратной совместимости.</p>
      </div>
      <GroupsPageContent />
    </div>
  )
}

function GroupsPageContent() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchGroupsPaged)

  const [dialog, setDialog] = useState(null) // { mode: 'create'|'edit', item }
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)

  // form state
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [isPremium, setIsPremium] = useState(false)

  function openCreate() {
    setName('')
    setDescription('')
    setIsPremium(false)
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setName(item.name || '')
    setDescription(item.description || '')
    setIsPremium(item.is_premium || false)
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = { name, description, is_premium: isPremium }
      if (dialog.mode === 'create') {
        await createGroup(payload)
        toast.success(t.create + ' OK')
      } else {
        await updateGroup(dialog.item.id, payload)
        toast.success(t.save + ' OK')
      }
      setDialog(null)
      paged.reload()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    setSaving(true)
    try {
      await deleteGroup(confirm.id)
      toast.success(t.delete + ' OK')
      setConfirm(null)
      paged.reload()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <PageHeader
        title={t.groupsTitle}
        actions={can('groups.write') ? (
          <Button onClick={openCreate}>+ {t.createGroup}</Button>
        ) : null}
      />

      <SearchPaginationBar
        search={paged.search}
        onSearch={paged.doSearch}
        page={paged.page}
        totalPages={paged.totalPages}
        onPageChange={paged.setPage}
        loading={paged.loading}
      />

      {paged.error && <p className="text-danger mb-4">{paged.error}</p>}

      {paged.items.length === 0 && !paged.loading ? (
        <EmptyState
          icon={UsersRound}
          title={t.noGroups || 'Нет групп'}
          description="Группы заменены на Тарифные планы — используйте /plans."
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th>ID</Th>
              <Th>{t.fieldGroupName}</Th>
              <Th>{t.fieldDescription}</Th>
              <Th>{t.fieldIsPremium}</Th>
              {can('groups.write') && <Th></Th>}
            </Tr>
          </Thead>
          <Tbody>
            {paged.items.map(g => (
              <Tr key={g.id}>
                <Td mono>{g.id}</Td>
                <Td>{g.name}</Td>
                <Td><span className="text-secondary">{g.description || '—'}</span></Td>
                <Td>{g.is_premium ? <Badge variant="accent">Premium</Badge> : <span className="text-tertiary">—</span>}</Td>
                {can('groups.write') && (
                  <Td>
                    <div className="flex gap-2">
                      <Button variant="ghost" size="sm" onClick={() => openEdit(g)}>{t.edit}</Button>
                      <Button variant="ghost" size="sm" onClick={() => setConfirm(g)} className="text-danger">{t.delete}</Button>
                    </div>
                  </Td>
                )}
              </Tr>
            ))}
          </Tbody>
        </Table>
      )}

      <CrudDialog
        open={!!dialog}
        title={dialog?.mode === 'create' ? t.createGroup : t.editGroup}
        onClose={() => setDialog(null)}
        onSubmit={handleSave}
        loading={saving}
      >
        <FormField label={t.fieldGroupName}>
          <TextInput value={name} onChange={setName} autoFocus />
        </FormField>
        <FormField label={t.fieldDescription}>
          <TextInput value={description} onChange={setDescription} />
        </FormField>
        <Checkbox label={t.fieldIsPremium} checked={isPremium} onChange={setIsPremium} />
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={`Delete group "${confirm?.name}"?`}
        onConfirm={handleDelete}
        onCancel={() => setConfirm(null)}
        loading={saving}
      />
    </div>
  )
}
