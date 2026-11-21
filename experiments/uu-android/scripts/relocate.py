"""Relocate dependency namespaces so the original APK's obfuscated libraries cannot collide."""
import struct
import zipfile
from pathlib import Path

MAPPINGS = {
    'okhttp3': 'io.ddremote.deps.http',
    'okio': 'io.ddremote.deps.io',
    'org.bouncycastle': 'io.ddremote.deps.bc',
}

def remap(text):
    """Java source uses dotted names, never JVM internal slash names."""
    for original, target in MAPPINGS.items():
        text = text.replace(original + '.', target + '.')
    return text

def internal_name(text):
    for original, target in MAPPINGS.items():
        text = text.replace(original.replace('.', '/') + '/', target.replace('.', '/') + '/')
    return text

def class_bytes(data):
    if data[:4] != b'\xca\xfe\xba\xbe': raise ValueError('Invalid class')
    count=struct.unpack_from('>H',data,8)[0]
    cursor,index=10,1
    out=bytearray(data[:10])
    sizes={3:4,4:4,5:8,6:8,7:2,8:2,9:4,10:4,11:4,12:4,15:3,16:2,17:4,18:4,19:2,20:2}
    while index<count:
        tag=data[cursor];cursor+=1;out.append(tag)
        if tag==1:
            size=struct.unpack_from('>H',data,cursor)[0];cursor+=2
            raw=data[cursor:cursor+size];cursor+=size
            # Names are ASCII; preserve JVM modified UTF8 bytes outside names.
            for original,target in MAPPINGS.items():
                raw=raw.replace((original.replace('.','/')+'/').encode(),(target.replace('.','/')+'/').encode())
                raw=raw.replace((original+'.').encode(),(target+'.').encode())
            out.extend(struct.pack('>H',len(raw)));out.extend(raw)
        else:
            size=sizes[tag];out.extend(data[cursor:cursor+size]);cursor+=size
            if tag in (5,6):index+=1
        index+=1
    out.extend(data[cursor:]);return out

def relocate(inputs,output):
    names=set()
    with zipfile.ZipFile(output,'w',zipfile.ZIP_DEFLATED) as out:
        for path in inputs:
            with zipfile.ZipFile(path) as source:
                for info in source.infolist():
                    name=info.filename
                    if not name.endswith('.class') or name.startswith('META-INF/') or name=='module-info.class':continue
                    newname=internal_name(name)
                    if newname in names:raise ValueError('Duplicate class '+newname)
                    names.add(newname);out.writestr(newname,class_bytes(source.read(info)))
    return len(names)
