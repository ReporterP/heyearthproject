// Placeholder for the «Добавить ноду» wizard — Task 2.14 replaces this file entirely.
import { Button, Dialog } from '../ui/index.js'

export default function AddNodeWizard({ onClose }) {
  return (
    <Dialog open onClose={onClose} title="Добавить ноду" maxWidth="max-w-md">
      <p className="text-sm text-secondary mb-6">Мастер добавления ноды появится в следующем обновлении админки.</p>
      <div className="flex justify-end">
        <Button variant="secondary" onClick={onClose}>Закрыть</Button>
      </div>
    </Dialog>
  )
}
