import {validateZones, videoRect} from './motion.mjs';
export function initMotionEditor(editor, api, tr) {
  const canvas=editor.querySelector('#zones-canvas'), video=editor.querySelector('#player'), text=editor.querySelector('#zones-json'), list=editor.querySelector('#zone-list');
  const enabled=editor.querySelector('#motion-enabled'),scope=editor.querySelector('#motion-scope'),scopeNote=editor.querySelector('#motion-scope-note');
  const message=editor.querySelector('#zones-message'), name=editor.querySelector('#zone-name'), exclude=editor.querySelector('#zone-exclude');
  let zones=validateZones(JSON.parse(text.value||'[]')||[]), points=[], editing=null, drawing=false, dirty=false, draftDirty=false;
  text.value=JSON.stringify(zones,null,2);
  const rect=()=>videoRect(canvas.clientWidth,canvas.clientHeight,video.videoWidth,video.videoHeight);
  const markDirty=()=>{dirty=true;text.value=JSON.stringify(zones,null,2);message.textContent=tr('Є незбережені зміни.');};
  function paint() {
    canvas.width=Math.max(1,Math.round(canvas.clientWidth*devicePixelRatio));canvas.height=Math.max(1,Math.round(canvas.clientHeight*devicePixelRatio));
    const c=canvas.getContext('2d');c.scale(devicePixelRatio,devicePixelRatio);const r=rect();c.lineWidth=2;c.font='13px system-ui';
    if(enabled.checked&&scope.value==='full'){c.strokeStyle='#b8ef8d';c.strokeRect(r.x+1,r.y+1,Math.max(0,r.width-2),Math.max(0,r.height-2));}
    const polygon=(vertices,color,label)=>{if(!vertices.length)return;c.beginPath();vertices.forEach((p,i)=>{const x=r.x+p.x*r.width,y=r.y+p.y*r.height;i?c.lineTo(x,y):c.moveTo(x,y);});if(vertices.length>2)c.closePath();c.strokeStyle=color;c.fillStyle=color+'22';c.fill();c.stroke();c.fillStyle=color;if(label)c.fillText(label,r.x+vertices[0].x*r.width+4,r.y+vertices[0].y*r.height+16);};
    c.globalAlpha=enabled.checked&&scope.value==='zones'?1:0.3;
    zones.forEach((zone,i)=>{if(i!==editing)polygon(zone.points,zone.exclude?'#ff8f87':'#b8ef8d',zone.name);});
    c.globalAlpha=1;
    if(editing!==null || points.length)polygon(points,'#80cfff',name.value);
  }
  function resetDraft() {editing=null;points=[];drawing=false;draftDirty=false;canvas.classList.remove('drawing');name.value=tr('Зона {number}',{number:zones.length+1});exclude.checked=false;editor.querySelector('#zone-form-title').textContent=tr('Нова зона');paint();}
  function updateScope(){scope.disabled=!enabled.checked;scopeNote.textContent=tr(!enabled.checked?'Виявлення руху вимкнено.':scope.value==='full'?'Аналізується весь кадр. Збережені зони та маски зараз не застосовуються.':zones.length?'Аналізуються збережені зони та маски.':'Зон немає — рух виявляється по всьому кадру.');paint();}
  enabled.addEventListener('change',()=>{dirty=true;updateScope();});scope.addEventListener('change',()=>{dirty=true;updateScope();});
  const button=(label,callback)=>{const b=document.createElement('button');b.type='button';b.textContent=tr(label);b.addEventListener('click',callback);return b;};
  function renderList() {
    list.replaceChildren();
    if(!zones.length){const p=document.createElement('p');p.className='muted';p.textContent=tr('Зон немає — рух виявляється по всьому кадру.');list.append(p);}
    zones.forEach((zone,index)=>{
      const row=document.createElement('div');row.className='zone-row';if(index===editing)row.classList.add('selected');
      const info=document.createElement('div'),title=document.createElement('strong'),kind=document.createElement('span');title.textContent=zone.name||tr('Без назви');kind.className='muted';kind.textContent=tr(zone.exclude?'Маска виключення':'Зона виявлення');info.append(title,kind);
      row.append(info,button('Редагувати',()=>{editing=index;points=zone.points.map(p=>({...p}));name.value=zone.name;exclude.checked=zone.exclude;drawing=false;draftDirty=false;canvas.classList.remove('drawing');editor.querySelector('#zone-form-title').textContent=tr('Редагування зони');renderList();paint();}),button('Видалити',()=>{zones.splice(index,1);resetDraft();markDirty();renderList();}));
      list.append(row);
    });updateScope();
  }
  text.addEventListener('input',()=>{dirty=true;});
  name.addEventListener('input',()=>{draftDirty=true;paint();});exclude.addEventListener('change',()=>{draftDirty=true;});
  editor.querySelector('#new-zone').onclick=()=>{resetDraft();renderList();drawing=true;canvas.classList.add('drawing');};
  editor.querySelector('#cancel-zone').onclick=()=>{resetDraft();renderList();};
  editor.querySelector('#draw-zone').onclick=()=>{points=[];drawing=true;draftDirty=true;canvas.classList.add('drawing');paint();};
  canvas.onclick=event=>{if(!drawing)return;const b=canvas.getBoundingClientRect(),r=rect(),x=event.clientX-b.left-r.x,y=event.clientY-b.top-r.y;if(x<0||y<0||x>r.width||y>r.height)return;points.push({x:x/r.width,y:y/r.height});draftDirty=true;paint();};
  editor.querySelector('#add-zone').onclick=()=>{
    try {if(!name.value.trim())throw Error('zone_name_required');const zone={name:name.value.trim(),exclude:exclude.checked,points:points.map(p=>({...p}))};const next=zones.slice();if(editing===null)next.push(zone);else next[editing]=zone;zones=validateZones(next);resetDraft();markDirty();renderList();}catch(e){message.textContent=tr(e.message);}
  };
  editor.querySelector('#clear-zones').onclick=()=>{zones=[];resetDraft();markDirty();renderList();};
  editor.querySelector('#apply-zones-json').onclick=()=>{try{zones=validateZones(JSON.parse(text.value));resetDraft();markDirty();renderList();}catch{message.textContent=tr('invalid_zones');}};
  for(const input of editor.querySelectorAll('input[type="number"]'))input.addEventListener('input',()=>{dirty=true;});
  editor.querySelector('#save-zones').onclick=async event=>{
    const button=event.currentTarget;
    try {
      if(draftDirty)throw Error('zone_draft_pending');
      if(text.value!==JSON.stringify(zones,null,2) && JSON.stringify(JSON.parse(text.value))!==JSON.stringify(zones))throw Error('zone_json_pending');
      for(const input of editor.querySelectorAll('input[type="number"]'))if(!input.reportValidity())return;
      button.disabled=true;
      await api(`/ui/zones/${editor.dataset.camera}`,{method:'POST',body:JSON.stringify({revision:Number(editor.dataset.revision),zones,motion_enabled:enabled.checked,motion_full_frame:scope.value==='full',threshold:Number(editor.querySelector('#motion-threshold').value),pre_seconds:Number(editor.querySelector('#motion-pre').value),post_seconds:Number(editor.querySelector('#motion-post').value)})});dirty=false;location.reload();
    }catch(e){message.textContent=tr(e.message);}finally{button.disabled=false;}
  };
  const resize=new ResizeObserver(paint);resize.observe(video);video.addEventListener('loadedmetadata',paint);
  window.addEventListener('beforeunload',event=>{if(dirty||draftDirty){event.preventDefault();event.returnValue='';}});
  window.addEventListener('pagehide',()=>resize.disconnect(),{once:true});
  resetDraft();renderList();
}
