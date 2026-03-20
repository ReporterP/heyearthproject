import sys
sys.stdout.reconfigure(encoding='utf-8')
import paramiko, os

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect('45.150.39.236', username='root', password='diU-~=cXpf23cE2:hzqZ')
sftp = ssh.open_sftp()

local_dist = os.path.join(os.path.dirname(__file__), 'admin_web', 'dist')
remote_base = '/var/www/heyearth'

for root, dirs, files in os.walk(local_dist):
    for f in files:
        local_path = os.path.join(root, f)
        rel_path = os.path.relpath(local_path, local_dist).replace(os.sep, '/')
        remote_path = remote_base + '/' + rel_path

        remote_dir = os.path.dirname(remote_path)
        try:
            sftp.stat(remote_dir)
        except FileNotFoundError:
            parts = remote_dir.split('/')
            for i in range(2, len(parts) + 1):
                d = '/'.join(parts[:i])
                try:
                    sftp.stat(d)
                except FileNotFoundError:
                    sftp.mkdir(d)

        sftp.put(local_path, remote_path)
        print(f'Uploaded: {rel_path}')

# Clean old assets
stdin, stdout, stderr = ssh.exec_command('ls /var/www/heyearth/assets/')
old_files = stdout.read().decode().strip().split('\n')

new_files = set()
assets_dir = os.path.join(local_dist, 'assets')
if os.path.isdir(assets_dir):
    for f in os.listdir(assets_dir):
        new_files.add(f)

for f in old_files:
    if f and f not in new_files:
        remote_path = f'/var/www/heyearth/assets/{f}'
        try:
            sftp.remove(remote_path)
            print(f'Removed old: {f}')
        except Exception as e:
            print(f'Failed to remove {f}: {e}')

sftp.close()
ssh.close()
print('Deploy complete!')
