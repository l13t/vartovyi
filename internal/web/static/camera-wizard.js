import {cameraPresets, presetURL, streamURL, safeStreamLabel} from './camera-presets.mjs';

export function initCameraWizard(form, api, tr) {
  const field=name=>form.elements.namedItem(name), steps=[...form.querySelectorAll('[data-wizard-step]')];
  const next=form.querySelector('#wizard-next'),back=form.querySelector('#wizard-back'),create=form.querySelector('#wizard-create'),message=form.querySelector('#wizard-message');
  const controller=new AbortController();
  let step=0,preset=null,busy=false,dirty=false,saved=false;
  const call=(path,body)=>api(path,{method:'POST',body:JSON.stringify(body),signal:AbortSignal.any([controller.signal,AbortSignal.timeout(35000)])});
  function sourceChanged(){
    const rtsp=field('source').value==='rtsp';
    form.querySelector('#wizard-rtsp').hidden=!rtsp;form.querySelector('#wizard-credentials').hidden=!rtsp;
  }
  function show(index){
    step=index;steps.forEach((s,i)=>s.hidden=i!==step);
    form.querySelectorAll('.wizard-progress li').forEach((li,i)=>{if(i===step)li.setAttribute('aria-current','step');else li.removeAttribute('aria-current');});
    back.hidden=step===0;next.hidden=step===3;create.hidden=step!==3;message.textContent='';
    steps[step].querySelector('h2').focus();
  }
  function validate(index){
    for(const input of steps[index].querySelectorAll('input,select')){
      if(!input.checkValidity()){
        for(let p=input.parentElement;p&&p!==form;p=p.parentElement)if(p.tagName==='DETAILS')p.open=true;
        input.reportValidity();return false;
      }
    }
    if(index===1){
      streamURL(field('url').value,field('source').value,field('username').value,field('password').value);
      if(field('source').value==='rtsp'&&field('substream').value.trim())streamURL(field('substream').value,'rtsp');
    }
    return true;
  }
  function payload(){
    const source=field('source').value;
    return {id:form.dataset.cameraId,revision:0,name:field('name').value.trim(),source,
      url:streamURL(field('url').value,source,field('username').value,field('password').value),
      substream:source==='rtsp'&&field('substream').value.trim()?streamURL(field('substream').value,'rtsp'):'',
      rtsp_transport:source==='rtsp'?field('rtsp_transport').value:'',transcode:source==='rtsp'&&field('transcode').checked,
      fps:Number(field('fps').value),shard_id:field('shard_id').value,group_id:field('group_id').value,
      mode:field('mode').value,enabled:field('enabled').checked,notify:field('notify').checked,
      motion_enabled:field('motion_enabled').checked,motion_full_frame:true,zones:[],
      threshold:Number(field('threshold').value),pre_seconds:Number(field('pre_seconds').value),post_seconds:Number(field('post_seconds').value),
      retention_days:14,max_segments:0,quota_bytes:0};
  }
  function review(){
    const c=payload(),list=form.querySelector('#wizard-review');list.replaceChildren();
    const label=name=>field(name).selectedOptions[0]?.textContent||'';
    const yes=value=>tr(value?'Увімкнений':'Вимкнений');
    for(const [name,value] of [
      ['Шаблон',tr(preset.name)],['Назва',c.name],['URL потоку',safeStreamLabel(c.url)],
      ['Джерело',label('source')],['Група камер',label('group_id')],['Вузол запису',c.shard_id],
      ['Режим',label('mode')],['Увімкнена',yes(c.enabled)],['Увімкнути виявлення руху',yes(c.motion_enabled)],
      ['Область перевірки',tr('Увесь кадр')],['Перекодовувати в H.264 (MJPEG / ESP32)',yes(c.transcode)],['FPS',String(c.fps)],
      ['Telegram про рух',yes(c.notify)]]){
      const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=tr(name);dd.textContent=value;list.append(dt,dd);
    }
  }
  const cards=form.querySelector('#camera-presets');
  for(const p of cameraPresets){
    const button=document.createElement('button');button.type='button';button.className='preset-card';button.dataset.preset=p.id;button.setAttribute('aria-pressed','false');
    const title=document.createElement('strong'),description=document.createElement('span');title.textContent=tr(p.name);description.textContent=tr(p.description);button.append(title,description);
    button.addEventListener('click',()=>{
      if(busy)return;
      preset=p;dirty=true;
      field('source').value=p.source;field('fps').value=p.fps;field('transcode').checked=p.transcode;field('rtsp_transport').value='tcp';
      field('url').value='';field('url').placeholder=presetURL(p,'camera.local');
      form.querySelector('#preset-description').textContent=tr(p.description);
      form.querySelector('#wizard-onvif').hidden=p.id!=='onvif';sourceChanged();
      cards.querySelectorAll('button').forEach(b=>b.setAttribute('aria-pressed',String(b===button)));message.textContent='';
    });cards.append(button);
  }
  field('source').addEventListener('change',sourceChanged);
  form.querySelector('#wizard-build-url').addEventListener('click',()=>{
    try{field('url').value=presetURL({...preset,source:field('source').value,path:field('source').value==='h264_tcp'?'':preset.path},field('host').value);dirty=true;message.textContent='';}catch(e){message.textContent=tr(e.message);}
  });
  next.addEventListener('click',()=>{
    try{
      if(step===0&&!preset)throw Error('wizard_select_preset');
      if(!validate(step))return;
      if(step===2)review();show(step+1);
    }catch(e){message.textContent=tr(e.message);}
  });
  back.addEventListener('click',()=>{if(!busy)show(step-1);});
  function setBusy(value){busy=value;for(const b of form.querySelectorAll('button'))b.disabled=value;}
  async function onvif(action){
    if(busy)return;setBusy(true);
    const note=form.querySelector('#wizard-onvif-message');note.textContent=tr('Зачекай…');
    try{
      if(action==='discover'){
        const result=await call('/api/v1/onvif/discover',{}),list=form.querySelector('#wizard-devices');list.replaceChildren();
        for(const device of result.items||[]){
          const b=document.createElement('button');b.type='button';b.textContent=safeStreamLabel(device.address);
          b.addEventListener('click',()=>{field('onvif_address').value=device.address;dirty=true;});list.append(b);
        }
        note.textContent=tr(list.childElementCount?'Обери знайдений пристрій і отримай профілі.':'Пристроїв не знайдено. Введи адресу вручну.');
      }else{
        const address=field('onvif_address').value.trim();
        if(!address)throw Error('wizard_onvif_address');
        const result=await call('/api/v1/onvif/profiles',{address,username:field('username').value,password:field('password').value});
        const list=form.querySelector('#wizard-profiles-list');list.replaceChildren();
        for(const profile of result.items||[]){
          // A profile response is untrusted device input; never inject its markup.
          let uri;try{uri=streamURL(profile.uri,'rtsp');}catch{continue;}
          const b=document.createElement('button');b.type='button';b.textContent=profile.name||profile.token;
          b.addEventListener('click',()=>{
            field('url').value=uri;field('source').value='rtsp';sourceChanged();dirty=true;
            list.querySelectorAll('button').forEach(button=>button.setAttribute('aria-pressed',String(button===b)));
            note.textContent=tr('Профіль обрано. Перевір URL потоку та продовжуй.');
          });list.append(b);
        }
        note.textContent=tr(list.childElementCount?'Обери відеопрофіль. Для MJPEG увімкни перекодування.':'Відеопрофілів не знайдено. Введи URL вручну.');
      }
    }catch(e){note.textContent=tr(e.name==='TimeoutError'?'wizard_timeout':e.message);}finally{setBusy(false);}
  }
  form.querySelector('#wizard-discover').addEventListener('click',()=>onvif('discover'));
  form.querySelector('#wizard-profiles').addEventListener('click',()=>onvif('profiles'));
  form.addEventListener('submit',async event=>{
    event.preventDefault();if(busy)return;
    if(step!==3){next.click();return;}
    try{
      for(let i=1;i<=2;i++){show(i);if(!validate(i))return;}show(3);
      setBusy(true);message.textContent=tr('Додаємо камеру…');
      const camera=await call('/api/v1/cameras',payload());saved=true;dirty=false;field('password').value='';field('url').value='';field('substream').value='';
      location.assign(`/cameras/${encodeURIComponent(camera.id)}`);
    }catch(e){message.textContent=tr(e.name==='TimeoutError'?'wizard_timeout':e.message);}finally{setBusy(false);}
  });
  form.addEventListener('input',()=>{dirty=true;});
  window.addEventListener('beforeunload',event=>{if(dirty&&!saved){event.preventDefault();event.returnValue='';}});
  window.addEventListener('pagehide',()=>{controller.abort();form.reset();field('url').value='';field('password').value='';field('substream').value='';form.querySelector('#wizard-profiles-list').replaceChildren();},{once:true});
}
