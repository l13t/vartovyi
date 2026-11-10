// Presets configure Vartovyi's input, not camera firmware. URLs remain editable.
export const cameraPresets = [
  {id:'rtsp',name:'RTSP / H.264',description:'IP-камера з готовим RTSP-потоком H.264.',source:'rtsp',port:'554',path:'/stream',fps:10,transcode:false},
  {id:'esp32',name:'ESP32 / MJPEG',description:'RTSP MJPEG із перекодуванням у H.264 на сервері. Шлях залежить від прошивки.',source:'rtsp',port:'8554',path:'/mjpeg/1',fps:10,transcode:true},
  {id:'pi',name:'Raspberry Pi / H.264 TCP',description:'Готовий сирий потік H.264 через TCP, наприклад із Raspberry Pi Zero 2 W.',source:'h264_tcp',port:'8554',path:'',fps:10,transcode:false},
  {id:'onvif',name:'ONVIF',description:'Знайди пристрій або введи його адресу, потім вибери відеопрофіль.',source:'rtsp',port:'554',path:'/stream',fps:10,transcode:false},
];

export function presetURL(preset, host) {
  const scheme=preset.source==='h264_tcp'?'tcp':'rtsp';
  let u;
  try { u=new URL(`${scheme}://${host.trim()}`); } catch { throw Error('wizard_invalid_host'); }
  if(!u.hostname || u.username || u.password || (u.pathname && u.pathname!=='/') || u.search || u.hash)throw Error('wizard_invalid_host');
  if(!u.port)u.port=preset.port;
  u.pathname=preset.path;
  return u.href;
}

export function streamURL(raw, source, username='', password='') {
  let u;
  try {u=new URL(raw.trim());}catch{throw Error('wizard_invalid_url');}
  const tcp=source==='h264_tcp';
  if(!u.hostname || (tcp?u.protocol!=='tcp:':!['rtsp:','rtsps:'].includes(u.protocol)))throw Error('wizard_invalid_url');
  if(tcp){
    if(u.username || u.password || !u.port || u.search || (u.pathname && u.pathname!=='/'))throw Error('wizard_invalid_url');
  }else if(username || password){
    if(username)u.username=username;
    u.password=password;
  }
  if(u.hash)throw Error('wizard_invalid_url');
  return u.href;
}

export function safeStreamLabel(raw) {
  try {const u=new URL(raw);return `${u.protocol}//${u.host}${u.pathname}`;}catch{return '';}
}
