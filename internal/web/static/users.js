import {generatePassword} from './password.mjs';
export function initUsers(api,tr) {
  const list=document.querySelector('#user-list');
  if(list){
    const boxes=Array.from(list.querySelectorAll('.user-select:not(:disabled)')),all=list.querySelector('#select-all-users'),remove=list.querySelector('#delete-selected-users');
    const dialog=document.querySelector('#delete-users-dialog'),confirm=dialog.querySelector('#confirm-delete-users'),error=dialog.querySelector('#delete-users-error');
    const selected=()=>boxes.filter(box=>box.checked);
    const update=()=>{const count=selected().length;remove.disabled=count===0;all.checked=count>0&&count===boxes.length;all.indeterminate=count>0&&count<boxes.length;all.disabled=boxes.length===0;list.querySelector('#selection-count').textContent=count?tr('Вибрано: {count}',{count}):tr('Нічого не вибрано');};
    all.onchange=()=>{boxes.forEach(box=>box.checked=all.checked);update();};boxes.forEach(box=>box.addEventListener('change',update));
    remove.onclick=()=>{const names=dialog.querySelector('#delete-users-names');names.replaceChildren();for(const box of selected()){const item=document.createElement('li');item.textContent=box.dataset.username;names.append(item);}error.textContent='';dialog.showModal();};
    dialog.querySelector('#cancel-delete-users').onclick=()=>dialog.close();
    confirm.onclick=async()=>{confirm.disabled=true;try{await api('/api/v1/users/bulk-delete',{method:'POST',body:JSON.stringify({ids:selected().map(box=>box.value)})});location.reload();}catch(e){error.textContent=e.message;}finally{confirm.disabled=false;}};
    update();
  }
  const form=document.querySelector('#user-form');if(!form)return;
  const password=form.querySelector('#user-password'),toggle=form.querySelector('#toggle-password'),result=document.querySelector('#user-created'),savedPassword=result.querySelector('#created-password');
  const setVisibility=(input,button,visible)=>{input.type=visible?'text':'password';button.textContent=tr(visible?'Приховати пароль':'Показати пароль');button.setAttribute('aria-pressed',String(visible));};
  toggle.onclick=()=>setVisibility(password,toggle,password.type==='password');
  form.querySelector('#generate-password').onclick=()=>{password.value=generatePassword();setVisibility(password,toggle,true);};
  form.addEventListener('submit',async event=>{
    event.preventDefault();const submit=form.querySelector('button[type="submit"]'),message=form.querySelector('#user-message');
    if(!form.reportValidity())return;
    submit.disabled=true;message.textContent='';
    try{
      const data=Object.fromEntries(new FormData(form));
      await api('/api/v1/users',{method:'POST',body:JSON.stringify(data)});
      result.querySelector('#created-username').value=data.username;
      savedPassword.value=data.password;setVisibility(savedPassword,result.querySelector('#toggle-created-password'),true);
      result.querySelector('#created-password-section').hidden=!data.password;
      form.reset();password.value='';form.hidden=true;result.hidden=false;result.querySelector('h2').tabIndex=-1;result.querySelector('h2').focus();
    }catch(e){message.textContent=e.message;}finally{submit.disabled=false;}
  });
  result.querySelector('#toggle-created-password').onclick=()=>setVisibility(savedPassword,result.querySelector('#toggle-created-password'),savedPassword.type==='password');
  result.querySelector('#copy-created-password').onclick=async()=>{const message=result.querySelector('#copy-password-message');try{await navigator.clipboard.writeText(savedPassword.value);message.textContent=tr('Пароль скопійовано.');}catch{savedPassword.select();message.textContent=tr('Скопіюй виділений пароль вручну.');}};
  window.addEventListener('pagehide',()=>{password.value='';savedPassword.value='';result.hidden=true;form.hidden=false;});
}
