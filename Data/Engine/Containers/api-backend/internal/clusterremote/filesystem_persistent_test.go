package clusterremote

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFilesystemPersistentNative(t *testing.T) {
	for _, mode := range []string{"root", "uuid syntax", "xfs", "unrelated mount", "escaped path", "missing fstab", "unsafe fstab", "symlink fstab", "duplicate root", "unstable source", "future ancestor", "future descendant", "noexec", "nofail", "automount option", "mount override", "automount", "mount dropin", "prefix dropin", "future unit", "unsafe unit", "generator override", "missing mask", "runtime mask", "bad device link", "wrong device", "regular device", "quota", "bind root", "current child mount", "stale mount", "loaded automount", "manager reload", "nonrunning manager", "degraded manager", "boolean generation", "negative generation", "wrong PID", "wrong bus", "pending root job", "loaded override", "loaded dropin", "loaded noexec", "fstab drift", "final file removal", "device drift", "manager drift", "mount drift", "lower availability"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			script := persistentNetworkLibraryScript + activeNetworkLibraryScript + systemdObservationLibraryScript + filesystemLibraryScript + persistentFilesystemLibraryScript + persistentFilesystemFixture
			out, err := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script, t.TempDir(), mode).CombinedOutput()
			good := mode == "root" || mode == "uuid syntax" || mode == "xfs" || mode == "unrelated mount" || mode == "escaped path" || mode == "lower availability" || mode == "degraded manager"
			if (err == nil) != good || ctx.Err() != nil {
				t.Fatalf("persistent native outcome: %v; %s", err, out)
			}
			if good {
				var value filesystemObservation
				if json.Unmarshal(out, &value) != nil || value.Version != 4 || value.Evidence.validate([]string{"/opt/Borealis", "/var/lib/longhorn"}) != nil {
					t.Fatal("invalid native persistent evidence")
				}
				if mode == "lower availability" && value.Evidence.Filesystems[0].AvailableBytes != 30<<30 {
					t.Fatal("availability minimum lost")
				}
			}
		})
	}
}

func TestFilesystemPersistentCommandBound(t *testing.T) {
	request, _ := filesystemFixture()
	request.RequirePersistent = true
	command, err := filesystemCommand(request)
	if err != nil || len(command) > 120<<10 || !strings.Contains(command, "observe_persistent_filesystems") || strings.Contains(command, request.MachineID) {
		t.Fatal("unbounded or wrong persistent command", len(command), err)
	}
}

// Only file metadata, declared mounts and loaded-manager inputs are synthetic.
// Real no-follow reads traverse temporary configuration trees. No installed
// mount, service, device, database or host configuration is changed.
const persistentFilesystemFixture = `
import pathlib, types, copy
ROOT, mode = sys.argv[1:]
EXPECTED_UID = os.getuid()
r = pathlib.Path(ROOT)
r.chmod(0o700)
os.umask(0o077)
paths = ["/opt/Borealis", "/var/lib/longhorn"]
uuid = "12345678-1234-1234-1234-123456789abc"
kind = "xfs" if mode == "xfs" else "ext4"
for area in BOOT_PATHS:
    (r/area).mkdir(parents=True, exist_ok=True)
for area in ("dev/disk/by-uuid", "etc/systemd/system-generators"):
    (r/area).mkdir(parents=True, exist_ok=True)
(r/"lib").symlink_to("usr/lib")
fstab = r/"etc/fstab"
text = "/dev/disk/by-uuid/"+uuid+" / "+kind+" defaults 0 1\n/swap.img none swap sw 0 0\n"
if mode == "uuid syntax": text = text.replace("/dev/disk/by-uuid/", "UUID=")
if mode == "duplicate root": text += text.splitlines()[0]+"\n"
if mode == "unstable source": text = text.replace("/dev/disk/by-uuid/"+uuid, "/dev/sda2")
if mode == "future ancestor": text += "/dev/other /var ext4 defaults 0 2\n"
if mode == "future descendant": text += "/dev/other /opt/Borealis/Engine ext4 defaults 0 2\n"
if mode == "unrelated mount": text += "/dev/other /home ext4 defaults 0 2\n"
if mode == "escaped path": text += r"/dev/other /home/with\040space ext4 defaults 0 2"+"\n"
if mode in ("noexec", "nofail", "automount option"):
    text = text.replace("defaults", {"noexec":"noexec", "nofail":"nofail", "automount option":"x-systemd.automount"}[mode])
fstab.write_text(text)
if mode == "missing fstab": fstab.unlink()
if mode == "unsafe fstab": fstab.chmod(0o666)
if mode == "symlink fstab": fstab.rename(r/"etc/other"); fstab.symlink_to("other")
unit = r/"run/systemd/generator/-.mount"
unit.write_text("[Unit]\nSourcePath=/etc/fstab\nBefore=local-fs.target\n[Mount]\nWhat=/dev/disk/by-uuid/"+uuid+"\nWhere=/\nType="+kind+"\n")
mask = r/"etc/systemd/system-generators/systemd-gpt-auto-generator"
mask.symlink_to("/dev/null")
if mode == "missing mask": mask.unlink()
if mode == "runtime mask":
    area=r/"run/systemd/system-generators"; area.mkdir(); (area/mask.name).symlink_to("/dev/null")
if mode == "generator override": (mask.parent/"foreign-generator").write_text("foreign")
if mode == "mount override": (r/"etc/systemd/system/-.mount").write_text(unit.read_text())
if mode == "automount": (r/"etc/systemd/system/var.automount").write_text("[Automount]\nWhere=/var\n")
if mode == "future unit": (r/"etc/systemd/system/opt-Borealis-Engine.mount").write_text("[Mount]\nWhere=/opt/Borealis/Engine\n")
if mode == "unsafe unit": unit.chmod(0o666)
if mode in ("mount dropin", "prefix dropin"):
    area=r/"etc/systemd/system"/("mount.d" if mode=="mount dropin" else "var-.mount.d")
    area.mkdir(); (area/"override.conf").write_text("[Mount]\nOptions=noexec\n")
device = r/"dev/sda2"; device.write_text("")
link = r/"dev/disk/by-uuid"/uuid
link.symlink_to("ab/cd/sda2" if mode == "bad device link" else "../../sda2")
real_stat = os.stat
stat_calls = 0
def fake_stat(path, *args, **kwargs):
    global stat_calls
    info = real_stat(path, *args, **kwargs)
    if str(path) in ("sda2", str(device)):
        stat_calls += 1
        fields = {name: getattr(info,name) for name in dir(info) if name.startswith("st_")}
        fields['st_mode'] = (stat.S_IFREG if mode == "regular device" else stat.S_IFBLK) | 0o600
        fields['st_rdev'] = os.makedev(8, 2 if mode == "wrong device" or mode == "device drift" and stat_calls > 1 else 1)
        return types.SimpleNamespace(**fields)
    return info
os.stat = fake_stat
raw = ("29 1 8:1 "+("/bind" if mode=="bind root" else "/")+" / rw,relatime - "+kind+" /dev/sda2 rw"+(" ,uquota" if mode=="quota" else "")+"\n").replace("rw ,", "rw,").encode()
if mode == "current child mount": raw += b"30 29 8:2 / /opt/Borealis/Engine rw - ext4 /dev/sdb2 rw\n"
native_read = fs_read
fs_read = lambda path,limit: raw if path == "/proc/self/mountinfo" else native_read(path,limit)
snapshots=0
def fixture_snapshot(paths):
    global snapshots
    snapshots += 1
    return {'version':3,'machine_id':'a'*32,'boot_id':'11111111-1111-4111-8111-111111111111','evidence':{
        'mount_namespace':21,'receipt':('b' if mode=='mount drift' and snapshots>1 else 'a')*64,
        'paths':[{'path':p,'ancestor':'/opt' if p.startswith('/opt') else '/var/lib','inode':123+i,'mount_id':29,'mount_root':'/','mount_point':'/','filesystem':'0000000000000001'} for i,p in enumerate(paths)],
        'filesystems':[{'id':'0000000000000001','device':'8:1','type':kind,'total_bytes':100<<30,'allocation_unit':4096,'total_inodes':1000000,'available_inodes':800000,'available_bytes':(30 if mode=='lower availability' and snapshots>2 else 70)<<30}]}}
fs_snapshot = fixture_snapshot
def value(signature, data): return {'type':signature,'data':data}
unit_props={name:value('s',v) for name,v in {'Id':'-.mount','LoadState':'loaded','ActiveState':'active','SubState':'mounted','FragmentPath':'/run/systemd/generator/-.mount','SourcePath':'/etc/fstab','UnitFileState':'generated'}.items()}
unit_props.update({name:value('b',False) for name in ('Transient','NeedDaemonReload')})
unit_props.update({'Names':value('as',['-.mount']),'DropInPaths':value('as',[]),'Job':value('(uo)',[0,'/']),'LoadError':value('(ss)',['','']),'Conditions':value('a(sbbsi)',[]),'Asserts':value('a(sbbsi)',[])})
mount_props={name:value('s',v) for name,v in {'What':'/dev/sda2','Where':'/','Type':kind,'Options':'rw,relatime','Result':'success'}.items()}
mount_props['ControlPID']=value('u',0)
if mode == 'loaded override': unit_props['FragmentPath']=value('s','/etc/systemd/system/-.mount')
if mode == 'loaded dropin': unit_props['DropInPaths']=value('as',['/etc/systemd/system/mount.d/override.conf'])
if mode == 'loaded noexec': mount_props['Options']=value('s','rw,noexec')
if mode == 'pending root job': unit_props['Job']=value('(uo)',[1,'/job/1'])
bus_calls=0
def fixture_reply(dest,path,interface,method,*params):
    global bus_calls
    bus_calls += 1
    def wire(signature,data): return {'type':signature,'data':[data]}
    if method=='GetId': return wire('s','0'*32 if mode=='wrong bus' else 'a'*32)
    if method=='GetNameOwner': return wire('s',':1.2' if mode=='manager drift' and bus_calls>10 else ':1.1')
    if method=='GetConnectionUnixProcessID': return wire('u',2 if mode=='wrong PID' else 1)
    if method=='Get':
        if params[-1]=='UnitPath': return wire('v',value('as',['/'+p for p in BOOT_PATHS]))
        if params[-1]=='SystemState': return wire('v',value('s','starting' if mode=='nonrunning manager' else 'degraded' if mode=='degraded manager' else 'running'))
        if params[-1]=='UnitsLoadTimestampMonotonic': return wire('v',value('t',False if mode=='boolean generation' else -1 if mode=='negative generation' else bus_calls if mode=='manager reload' else 0))
    if method=='GetAll':
        if params[-1]=='org.freedesktop.systemd1.Unit': return wire('a{sv}',unit_props)
        if params[-1]=='org.freedesktop.systemd1.Mount':
            if mode=='fstab drift': fstab.write_text(text+'#changed\n')
            if mode=='final file removal' and snapshots>=3: unit.unlink()
            return wire('a{sv}',mount_props)
    if method=='ListUnitsByPatterns':
        rows=[['-.mount','Root','loaded','active','mounted','','/org/freedesktop/systemd1/unit/_2d_2emount',0,'','/']]
        if mode in ('stale mount','loaded automount'):
            rows.append(['var.'+('automount' if mode=='loaded automount' else 'mount'),'Old','loaded','inactive','dead','','/old',0,'','/'])
        return wire('a(ssssssouso)',rows)
    raise ValueError('unexpected fixture request')
bus_reply=fixture_reply
print(json.dumps(observe_persistent_filesystems(paths),separators=(',',':')))
`
