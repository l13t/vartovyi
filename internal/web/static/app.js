import {initMotionEditor} from './motion-editor.js';
import {initUsers} from './users.js';
import {initCameraWizard} from './camera-wizard.js';
const language = document.documentElement.lang;
const translations = await fetch(`/static/${language === 'en' ? 'en' : 'uk'}.json`).then(r => r.json());
function tr(key, values = {}) { let text = translations[key] || key; for (const [name,value] of Object.entries(values)) text = text.replaceAll(`{${name}}`, String(value)); return text; }
const csrf = document.querySelector('meta[name="csrf-token"]')?.content;
async function api(path, options = {}) {
  const response = await fetch(path, {...options, headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf, ...options.headers}});
  if (!response.ok) { let code = `HTTP ${response.status}`; try {code = (await response.json()).error.code;} catch {} throw new Error(tr(code)); }
  return response.status === 204 ? null : response.json();
}
const wizard=document.querySelector('#camera-wizard');
if(wizard)initCameraWizard(wizard,api,tr);
const players = new WeakMap();
function play(video, source, live = false) {
  players.get(video)?.destroy(); players.delete(video);
  if (live && !video.canPlayType('application/vnd.apple.mpegurl') && window.Hls?.isSupported()) {
    const hls = new window.Hls({maxBufferLength: 20}); hls.loadSource(source); hls.attachMedia(video); players.set(video,hls);
  } else {video.src = source;}
  video.play().catch(() => {});
}
document.querySelectorAll('video[data-live]').forEach(v => play(v, v.dataset.live, true));
if (document.querySelector('[data-status]')) {
  const events = new EventSource('/api/v1/stream');
  events.addEventListener('status', e => {try {for (const s of JSON.parse(e.data).items) {document.querySelectorAll('[data-status]').forEach(el => {if(el.dataset.status===s.camera_id) {el.textContent=({recording:tr("Запис триває"),offline:tr("Немає зв’язку"),disabled:tr("Вимкнена"),disk_full:tr("Архів заповнений"),detector_degraded:tr("Запис · детектор недоступний")})[s.state]||s.state;el.dataset.state=s.state;}});}} catch {}});
  window.addEventListener('pagehide', () => events.close(), {once:true});
}
document.addEventListener('htmx:responseError', e => {const el=e.detail.elt.querySelector('.form-message');if(el) el.textContent=tr('Не вдалося зберегти. Перевір налаштування або онови сторінку.');});
const archive = document.querySelector('#archive');
if (archive) {
  const camera=archive.dataset.camera, start=document.querySelector('#archive-start'), end=document.querySelector('#archive-end'), player=document.querySelector('#player');
  const local = d => new Date(d.getTime()-d.getTimezoneOffset()*60000).toISOString().slice(0,16);
  const requested=new URL(location.href).searchParams.get('at'); const until=requested?new Date(new Date(requested).getTime()+60000):new Date();
  end.value=local(until);start.value=local(new Date(until.getTime()-3600000));
  const interval=()=>{const a=new Date(start.value),b=new Date(end.value);if(!Number.isFinite(+a)||!Number.isFinite(+b)||b<=a)throw Error(tr('Обери коректний інтервал'));return{start:a.toISOString(),end:b.toISOString()};};
  document.querySelector('#go-live').onclick=()=>play(player,player.dataset.live,true);
  document.querySelector('#load-timeline').onclick=async()=>{try {const q=new URLSearchParams(interval()), data=await api(`/api/v1/cameras/${camera}/timeline?${q}`),target=document.querySelector('#timeline');target.replaceChildren();let previous;
    for(const segment of data.items){if(previous&&new Date(segment.start)-new Date(previous.end)>2000){const gap=document.createElement('span');gap.textContent=tr(' · прогалина · ');target.append(gap);}const button=document.createElement('button');button.textContent=new Date(segment.start).toLocaleTimeString(language);button.onclick=()=>{const t=new URLSearchParams({start:segment.start,end:segment.end});play(player,`/media/${camera}/playback/clip.mp4?${t}`);};target.append(button);previous=segment;}
    document.querySelector('#archive-message').textContent=data.items.length?tr('{count} сегментів',{count:data.items.length}):tr('За цей час записів немає');
  }catch(e){document.querySelector('#archive-message').textContent=e.message;}};
  document.querySelector('#export-video')?.addEventListener('click', async()=>{const message=document.querySelector('#export-message');try{const job=await api('/api/v1/exports',{method:'POST',headers:{'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({camera_id:camera,...interval()})});message.textContent=tr('Готуємо експорт…');let stopped=false;window.addEventListener('pagehide',()=>{stopped=true;},{once:true});for(let i=0;i<180&&!stopped;i++){await new Promise(r=>setTimeout(r,2000));const status=await api(`/api/v1/exports/${job.id}`);if(status.status==='failed')throw Error(status.error);if(status.status==='ready'){const a=document.createElement('a');a.textContent=tr('Завантажити MP4');a.href=`/api/v1/exports/${job.id}/download`;message.replaceChildren(a);return;}}message.textContent=tr('Експорт {id} ще обробляється',{id:job.id});}catch(e){message.textContent=e.message;}});
}
const editor=document.querySelector('#zones-editor');
if(editor)initMotionEditor(editor,api,tr);
initUsers(api,tr);
document.querySelector('#discover')?.addEventListener('click',async()=>{try{document.querySelector('#discovery-result').textContent=JSON.stringify(await api('/api/v1/onvif/discover',{method:'POST',body:'{}'}),null,2);}catch(e){document.querySelector('#discovery-result').textContent=e.message;}});
for(const [id,path,target] of [['onvif-profiles','/api/v1/onvif/profiles','profiles-result']]){document.getElementById(id)?.addEventListener('submit',async e=>{e.preventDefault();try{const data=Object.fromEntries(new FormData(e.target));const result=await api(path,{method:'POST',body:JSON.stringify(data)});document.getElementById(target).textContent=result?JSON.stringify(result,null,2):tr('Збережено');}catch(error){document.getElementById(target).textContent=error.message;}});}


for(const form of document.querySelectorAll('[data-camera-form]')){
 const source=form.querySelector('[data-source-selector]'),options=form.querySelector('[data-rtsp-options]');
 const update=()=>{const rtsp=source.value==='rtsp';options.hidden=!rtsp;for(const field of options.querySelectorAll('input,select'))field.disabled=!rtsp;};
 source.addEventListener('change',update);update();
}
