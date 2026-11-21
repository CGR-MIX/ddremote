"""Local UU APK adapter build. Original files stay outside the distributable source tree."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import xml.etree.ElementTree as ET
import zipfile
from relocate import remap, relocate
from protocol_patch import adapt

ORIGINAL_SHA = '19252db1dd384f25db000b334e6f77616b9dfa26b2e416ce5383038e973e219f'
APKTOOL_SHA = 'dbf930b076c6b9be08d57c449cacefc3bdd6b71ebd59b3066fc0e1f5b14f9423'
NS = 'http://schemas.android.com/apk/res/android'
ET.register_namespace('android', NS)

def sha(path):
    return hashlib.file_digest(open(path, 'rb'), 'sha256').hexdigest()

def manifest(with_test=False):
    root = ET.Element('manifest', {'package':'io.ddremote.uuadapter'})
    for permission in ('INTERNET','ACCESS_NETWORK_STATE','ACCESS_WIFI_STATE'):
        ET.SubElement(root,'uses-permission',{f'{{{NS}}}name':'android.permission.'+permission})
    attrs = {'name':'android.app.Application','label':'DD远程 · UU适配',
             'icon':'@android:drawable/ic_menu_slideshow','allowBackup':'false',
             'theme':'@style/Theme.AppCompat.Light.NoActionBar','supportsRtl':'true',
             'usesCleartextTraffic':'false','networkSecurityConfig':'@xml/dd_network_security','extractNativeLibs':'true'}
    app=ET.SubElement(root,'application',{f'{{{NS}}}{k}':v for k,v in attrs.items()})
    home=ET.SubElement(app,'activity',{f'{{{NS}}}name':'com.limelight.ddremote.UuAdapterHome',f'{{{NS}}}exported':'true'})
    intent=ET.SubElement(home,'intent-filter')
    ET.SubElement(intent,'action',{f'{{{NS}}}name':'android.intent.action.MAIN'})
    ET.SubElement(intent,'category',{f'{{{NS}}}name':'android.intent.category.LAUNCHER'})
    ET.SubElement(app,'activity',{f'{{{NS}}}name':'com.limelight.ddremote.UuInputLab',f'{{{NS}}}exported':'false',f'{{{NS}}}windowSoftInputMode':'adjustResize'})
    ET.SubElement(app,'activity',{f'{{{NS}}}name':'com.limelight.ddremote.UuStreamActivity',f'{{{NS}}}exported':'false',f'{{{NS}}}screenOrientation':'sensorLandscape',f'{{{NS}}}configChanges':'keyboard|keyboardHidden|orientation|screenSize',f'{{{NS}}}theme':'@style/Theme.AppCompat.Light.NoActionBar'})
    ET.SubElement(app,'service',{f'{{{NS}}}name':'com.limelight.ddremote.NetworkLifecycleService',f'{{{NS}}}exported':'false',f'{{{NS}}}stopWithTask':'false'})
    if with_test:
        ET.SubElement(root,'instrumentation',{f'{{{NS}}}name':'com.limelight.ddremote.StreamInstrumentation',f'{{{NS}}}targetPackage':'io.ddremote.uuadapter'})
    ET.indent(root)
    return ET.tostring(root,encoding='utf-8',xml_declaration=True)

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--stage',required=True)
    parser.add_argument('--java-home',required=True)
    parser.add_argument('--sdk',required=True)
    parser.add_argument('--apk',required=True)
    parser.add_argument('--apktool',required=True)
    parser.add_argument('--with-test-fixture',action='store_true')
    a=parser.parse_args()
    repo=Path(__file__).resolve().parents[3]
    stage=Path(a.stage).resolve()
    if not stage.is_absolute() or stage==Path(stage.anchor) or stage==repo:
        raise SystemExit('Dedicated absolute staging directory required')
    apk,tool=Path(a.apk).resolve(),Path(a.apktool).resolve()
    if sha(apk)!=ORIGINAL_SHA or sha(tool)!=APKTOOL_SHA:
        raise SystemExit('Pinned input hash mismatch')
    for folder in ('tmp','framework','classes','dex','java'):(stage/folder).mkdir(parents=True,exist_ok=True)
    java=Path(a.java_home)/'bin/java.exe'
    sdk=Path(a.sdk)
    decoded=stage/'decoded'
    def run(name,args):
        with (stage/(name+'.log')).open('w',encoding='utf-8') as log:
            completed=subprocess.run([str(v) for v in args],cwd=stage,stdout=log,stderr=subprocess.STDOUT,creationflags=0x08000000)
        if completed.returncode:
            raise RuntimeError(name+' failed; see '+str(stage/(name+'.log')))
    jargs=[java,'-Xmx640m','-Djava.io.tmpdir='+str(stage/'tmp')]
    if not (decoded/'apktool.yml').exists():
        run('decode',jargs+['-jar',tool,'decode','-j','1','-p',stage/'framework','-o',decoded,apk])
    metadata=decoded/'apktool.yml'
    original_yaml=metadata.read_text(encoding='utf-8')
    # This cache belongs to this pinned input. It is intentionally outside release/source archives.
    (decoded/'AndroidManifest.xml').write_bytes(manifest(a.with_test_fixture))
    (decoded/'res/xml').mkdir(exist_ok=True)
    (decoded/'res/xml/dd_network_security.xml').write_text('<network-security-config><base-config cleartextTrafficPermitted="false"/><domain-config cleartextTrafficPermitted="true"><domain>127.0.0.1</domain><domain>127.0.0.2</domain>'+('<domain>10.0.2.2</domain>' if a.with_test_fixture else '')+'</domain-config></network-security-config>',encoding='utf-8')
    import re
    metadata.write_text(re.sub(r'(?m)^  versionName:.*$', '  versionName: 0.3.4-mobile-lab',re.sub(r'(?m)^  versionCode:.*$','  versionCode: 5',original_yaml)),encoding='utf-8')
    run('build-adapter-base',jargs+['-jar',tool,'build','-j','1','-p',stage/'framework','-o',stage/'adapter-base.apk',decoded])
    upstream=repo/'apps/android/app/src/main/java'
    selected=list((upstream/'com/limelight/nvstream').rglob('*.java'))
    selected=[p for p in selected if '/mdns/' not in p.as_posix() and '/wol/' not in p.as_posix()]
    selected += [upstream/'com/limelight'/p for p in ('LimeLog.java','binding/audio/AndroidAudioRenderer.java','binding/crypto/AndroidCryptoProvider.java','binding/input/KeyboardTranslator.java')]
    own=repo/'experiments/uu-android/src'
    inputs=[(p,p.relative_to(upstream)) for p in selected]+[(p,p.relative_to(own)) for p in sorted(own.rglob('*.java'))]
    if a.with_test_fixture:
        testsrc=repo/'experiments/uu-android/testsrc'
        inputs += [(p,p.relative_to(testsrc)) for p in sorted(testsrc.rglob('*.java'))]
    # Compile in a fresh owned output; previous Java artifacts cannot enter this APK.
    import tempfile
    classout=Path(tempfile.mkdtemp(prefix='classes-',dir=stage))
    dexout=Path(tempfile.mkdtemp(prefix='dex-',dir=stage))
    sources=[]
    for path,relative in inputs:
        target=stage/'java'/relative;target.parent.mkdir(parents=True,exist_ok=True)
        target.write_text(remap(adapt(relative,path.read_text(encoding='utf-8-sig'))),encoding='utf-8');sources.append(target)
    depsmanifest=json.loads((repo/'experiments/uu-android/dependencies.json').read_text(encoding='utf-8-sig'))
    dependencies=[]
    for item in depsmanifest:
        path=repo/'.tools/uu-adapter-deps'/item['name']
        if sha(path)!=item['sha256']:raise RuntimeError('Dependency hash mismatch '+item['name'])
        dependencies.append(path)
    relocated=stage/'protocol-dependencies.jar'
    relocate(dependencies,relocated)
    androidjar=sdk/'platforms/android-37.0/android.jar'
    if not androidjar.is_file(): raise RuntimeError('Android API 37 platform missing')
    run('javac',[Path(a.java_home)/'bin/javac.exe','-encoding','UTF-8','-source','8','-target','8','-classpath',str(androidjar)+os.pathsep+str(relocated),'-d',classout]+sources)
    classesjar=stage/'adapter-classes.jar'
    with zipfile.ZipFile(classesjar,'w',zipfile.ZIP_DEFLATED) as out:
        for path in sorted(classout.rglob('*.class')):out.write(path,path.relative_to(classout).as_posix())
    run('d8',jargs+['-cp',sdk/'build-tools/37.0.0/lib/d8.jar','com.android.tools.r8.D8','--min-api','26','--lib',androidjar,'--output',dexout,classesjar,relocated])
    output=stage/'DDRemote-UU-adapter-lab-unsigned.apk'
    with zipfile.ZipFile(stage/'adapter-base.apk') as source,zipfile.ZipFile(output,'w',zipfile.ZIP_DEFLATED) as out:
        for info in source.infolist():
            if info.filename=='classes3.dex' or info.filename.startswith('META-INF/') or info.filename.startswith('assets/dexopt/'):
                continue
            out.writestr(info,source.read(info))
        dexfiles=sorted(dexout.glob('classes*.dex'),key=lambda p:1 if p.stem=='classes' else int(p.stem[7:]))
        for i,dex in enumerate(dexfiles,3):out.write(dex,'classes'+str(i)+'.dex')
        for abi in ('arm64-v8a','x86_64'):
            out.write(repo/'apps/android/app/src/main/jniLibs'/abi/'libddnetwork.so','lib/'+abi+'/libddnetwork.so')
            with zipfile.ZipFile(repo/'dist/DDRemote-0.2.1-preview-android.apk') as native:
                name='lib/'+abi+'/libmoonlight-core.so'
                out.writestr(name,native.read(name))
    with zipfile.ZipFile(output) as result:
        assert all('classes'+suffix+'.dex' in result.namelist() for suffix in ('','2','3'))
        assert 'assets/fullkeyboard/win_full_keyboard_main.json' in result.namelist()
    report={'inputSha256':ORIGINAL_SHA,'apktoolSha256':APKTOOL_SHA,'outputSha256':sha(output),'bytes':output.stat().st_size,
            'package':'io.ddremote.uuadapter','version':'0.3.4-mobile-lab','originalApplicationExecuted':False,
            'testInstrumentationIncluded':a.with_test_fixture,'streamingAdapted':'implemented; runtime verification is recorded separately in evidence/','originalResourcesRetained':True,'originalDexRetainedAsReassembled':True,
            'componentScope':['DD account network','original UU input view','original keyboard assets','original UU StreamerSurfaceView','DD H264 MediaCodec renderer','Moonlight protocol and audio library (no Game activity)','DD touch/quality controls']}
    (stage/'build-report.json').write_text(json.dumps(report,indent=2),encoding='utf-8')
    print(json.dumps(report,indent=2))
    print(output)

if __name__=='__main__':main()
