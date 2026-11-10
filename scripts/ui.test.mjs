import test from 'node:test';
import assert from 'node:assert/strict';
import {webcrypto} from 'node:crypto';
import {generatePassword} from '../internal/web/static/password.mjs';
import {validateZones,videoRect} from '../internal/web/static/motion.mjs';
import {cameraPresets,presetURL,streamURL,safeStreamLabel} from '../internal/web/static/camera-presets.mjs';

test('password generator uses 24 unbiased characters from secure random bytes',()=>{
 let called=false;
 const password=generatePassword(bytes=>{called=true;for(let i=0;i<bytes.length;i++)bytes[i]=i;return bytes;});
 assert.ok(called);assert.equal(password,'ABCDEFGHIJKLMNOPQRSTUVWX');
 assert.match(generatePassword(bytes=>webcrypto.getRandomValues(bytes)),/^[A-Za-z0-9_-]{24}$/);
});
test('zone editing validates and clones saved include/exclude polygons',()=>{
 const zone={name:'Двір',exclude:false,points:[{x:0,y:0},{x:1,y:0},{x:1,y:1}]};
 const result=validateZones([zone,{...zone,exclude:true}]);
 result[0].points[0].x=.2;assert.equal(zone.points[0].x,0);assert.equal(result[1].exclude,true);
 for(const invalid of [[{...zone,points:[]}],[{...zone,points:[{x:NaN,y:0},...zone.points]}],Array(33).fill(zone),[{...zone,points:[{x:2,y:0},...zone.points]}]])assert.throws(()=>validateZones(invalid));
});
test('video coordinates exclude letterboxing and survive zero dimensions',()=>{
 assert.deepEqual(videoRect(960,540,1920,1080),{x:0,y:0,width:960,height:540});
 assert.deepEqual(videoRect(960,540,640,480),{x:120,y:0,width:720,height:540});
 assert.deepEqual(videoRect(0,0,0,0),{x:0,y:0,width:0,height:0});
});
test('camera presets build editable URLs with correct codec and transport defaults',()=>{
 const esp=cameraPresets.find(p=>p.id==='esp32'),pi=cameraPresets.find(p=>p.id==='pi');
 assert.equal(esp.transcode,true);assert.equal(pi.transcode,false);assert.equal(pi.source,'h264_tcp');
 assert.equal(presetURL(esp,'camera.local'),'rtsp://camera.local:8554/mjpeg/1');
 assert.equal(presetURL(esp,'camera.local:9554'),'rtsp://camera.local:9554/mjpeg/1');
 assert.equal(presetURL(pi,'[2001:db8::1]'),'tcp://[2001:db8::1]:8554');
 for(const host of ['', 'rtsp://camera/stream','user:password@camera','camera/path','camera?token=secret'])assert.throws(()=>presetURL(esp,host));
});
test('wizard credentials are URL-encoded and omitted with query strings from review',()=>{
 const raw=streamURL('rtsp://camera.local/stream?key=secret','rtsp','operator@home','p@ss:/?#');
 const parsed=new URL(raw);
 assert.equal(decodeURIComponent(parsed.username),'operator@home');assert.equal(decodeURIComponent(parsed.password),'p@ss:/?#');
 assert.equal(safeStreamLabel(raw),'rtsp://camera.local/stream');
 assert.equal(streamURL('rtsp://embedded:password@camera/stream','rtsp'),'rtsp://embedded:password@camera/stream');
 for(const raw of ['https://camera/stream','file:///etc/passwd','rtsp://camera/stream#secret'])assert.throws(()=>streamURL(raw,'rtsp'));
 for(const raw of ['tcp://camera','tcp://user:pass@camera:8554','tcp://camera:8554/path'])assert.throws(()=>streamURL(raw,'h264_tcp'));
});
