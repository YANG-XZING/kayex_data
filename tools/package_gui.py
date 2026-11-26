"""Visual selector and external configuration editor for Kayex packaging."""
import json, os, queue, subprocess, sys, threading
from datetime import datetime
from pathlib import Path
import tkinter as tk
from tkinter import ttk, filedialog, messagebox
from tkinter.scrolledtext import ScrolledText
import package

LABELS={'bigdata_consumer':'PLC 数据消费程序','hmi_consumer':'HMI 数据消费程序','kx-opc-client':'OPC 采集客户端','kx-opc-web':'Web 网站（包含当前前端）','tdengine':'TDengine 安装包 + 初始化 SQL','rabbitmq':'RabbitMQ + Erlang 等离线包'}
FILES=['settings.json','bigdata_consumer/environment.json','hmi_consumer/environment.json','kx-opc-client/config.yaml','kx-opc-client/furnaces.json','kx-opc-web/config.yaml','tdengine/database.json','tdengine/taos.cfg','rabbitmq/install.json','rabbitmq/rabbitmq.conf','rabbitmq/rabbitmq-env.conf']

class App:
 def __init__(self,root):
  self.root=root;root.title('Kayex Linux 可视化打包');root.geometry('1040x800');root.minsize(880,660)
  self.events=queue.Queue();self.busy=False;self.current_file=None;self.loaded_profile=None
  self.profile=tk.StringVar(value=str(package.ROOT/'profiles/site'));self.output=tk.StringVar(value=str(package.ROOT/'dist'));self.version=tk.StringVar(value=datetime.now().strftime('%Y%m%d%H%M%S'))
  self.selected={name:tk.BooleanVar(value=name in package.APPS) for name in package.COMPONENTS}
  style=ttk.Style();style.configure('Title.TLabel',font=('Microsoft YaHei UI',17,'bold'));style.configure('Hint.TLabel',foreground='#526078')
  wrap=ttk.Frame(root,padding=20);wrap.pack(fill='both',expand=True)
  ttk.Label(wrap,text='部署组件打包',style='Title.TLabel').pack(anchor='w')
  ttk.Label(wrap,text='修改外置配置 → 勾选组件 → 校验并打包。每个组件生成独立 Linux x86_64 安装包。',style='Hint.TLabel').pack(anchor='w',pady=(4,14))
  paths=ttk.Frame(wrap);paths.pack(fill='x')
  for row,(label,var,kind) in enumerate([('配置目录',self.profile,'profile'),('输出目录',self.output,'output')]):
   ttk.Label(paths,text=label,width=10).grid(row=row,column=0,sticky='w',pady=4)
   ttk.Entry(paths,textvariable=var).grid(row=row,column=1,sticky='ew',padx=8)
   ttk.Button(paths,text='选择目录',command=lambda k=kind:self.browse(k)).grid(row=row,column=2)
  paths.columnconfigure(1,weight=1)
  bar=ttk.Frame(wrap);bar.pack(fill='x',pady=8)
  ttk.Label(bar,text='版本号',width=10).pack(side='left');ttk.Entry(bar,textvariable=self.version,width=28).pack(side='left',padx=8)
  self.newbtn=ttk.Button(bar,text='新建配置目录',command=self.new_profile);self.newbtn.pack(side='right')
  notebook=ttk.Notebook(wrap);notebook.pack(fill='both',expand=True,pady=8)
  selection=ttk.Frame(notebook,padding=16);notebook.add(selection,text='① 选择组件')
  for i,(name,label) in enumerate(LABELS.items()):
   group=ttk.LabelFrame(selection,text=package.APP_NAMES.get(name,name),padding=14);group.grid(row=i//2,column=i%2,sticky='nsew',padx=8,pady=8)
   ttk.Checkbutton(group,text=label,variable=self.selected[name]).pack(anchor='w')
  selection.columnconfigure(0,weight=1);selection.columnconfigure(1,weight=1)
  actions=ttk.Frame(selection);actions.grid(row=3,column=0,columnspan=2,sticky='w',padx=8,pady=12)
  ttk.Button(actions,text='全选',command=lambda:self.select_all(True)).pack(side='left')
  ttk.Button(actions,text='清空',command=lambda:self.select_all(False)).pack(side='left',padx=8)
  ttk.Label(selection,text='程序部署到 TD/消费服务器；RabbitMQ 包部署到 MQ 服务器。\n打包只在本机编译和归档，不连接现场设备，不执行远端 SQL。',style='Hint.TLabel').grid(row=4,column=0,columnspan=2,sticky='w',padx=8,pady=8)
  editing=ttk.Frame(notebook,padding=12);notebook.add(editing,text='② 外置配置')
  self.filevar=tk.StringVar(value=FILES[0]);self.combo=ttk.Combobox(editing,textvariable=self.filevar,values=FILES,state='readonly');self.combo.pack(fill='x');self.combo.bind('<<ComboboxSelected>>',lambda e:self.switch_file())
  ttk.Label(editing,text='可直接修改外部文件；切换文件或开始校验/打包时自动保存当前编辑内容。CHANGE_ME 必须填写。',style='Hint.TLabel').pack(anchor='w',pady=6)
  self.editor=ScrolledText(editing,undo=True,font=('Consolas',10),wrap='none');self.editor.pack(fill='both',expand=True)
  self.savebtn=ttk.Button(editing,text='保存当前配置',command=lambda:self.save_file(show=True));self.savebtn.pack(anchor='e',pady=(8,0))
  logs=ttk.Frame(notebook,padding=10);notebook.add(logs,text='③ 日志');self.log=ScrolledText(logs,font=('Consolas',10),state='disabled');self.log.pack(fill='both',expand=True)
  self.notebook=notebook
  foot=ttk.Frame(wrap);foot.pack(fill='x',pady=(8,0));self.status=tk.StringVar(value='就绪：先填写所选组件配置，再进行校验。');ttk.Label(foot,textvariable=self.status).pack(side='left')
  self.buildbtn=ttk.Button(foot,text='开始打包',command=lambda:self.launch('build'));self.buildbtn.pack(side='right',padx=(8,0))
  self.checkbtn=ttk.Button(foot,text='校验所选配置',command=lambda:self.launch('check'));self.checkbtn.pack(side='right')
  self.progress=ttk.Progressbar(wrap,mode='indeterminate');self.progress.pack(fill='x',pady=(10,0))
  self.open_profile();root.after(100,self.poll)
 def select_all(self,value):
  if not self.busy:
   for var in self.selected.values():var.set(value)
 def browse(self,kind):
  if self.busy:return
  if kind=='profile':self.save_file()
  folder=filedialog.askdirectory(title='选择'+('配置' if kind=='profile' else '输出')+'目录')
  if folder:
   (self.profile if kind=='profile' else self.output).set(folder)
   if kind=='profile':self.open_profile()
 def new_profile(self):
  if self.busy:return
  parent=filedialog.askdirectory(title='选择新配置目录的上级目录')
  if not parent:return
  folder=Path(parent)/('site-'+datetime.now().strftime('%Y%m%d%H%M%S'))
  try:package.init_profile(folder);self.save_file();self.profile.set(str(folder));self.open_profile()
  except Exception:messagebox.showerror('创建失败','无法创建配置模板，请检查目录权限。')
 def open_profile(self):
  p=Path(self.profile.get())
  if not p.exists():package.init_profile(p)
  if not (p/'settings.json').exists():
   self.current_file=None;messagebox.showerror('配置目录无效','所选目录中没有 settings.json，请选择配置目录或点击新建配置目录。');return
  package.ensure_component_templates(p);self.loaded_profile=p;self.current_file=None;self.switch_file()
 def switch_file(self):
  self.save_file();self.current_file=self.loaded_profile/self.filevar.get()
  try:text=self.current_file.read_text(encoding='utf-8-sig')
  except OSError:text=''
  self.editor.delete('1.0','end');self.editor.insert('1.0',text);self.editor.edit_modified(False)
 def save_file(self,show=False):
  if not self.current_file:return True
  if not self.editor.edit_modified() and not show:return True
  try:package.write(self.current_file,self.editor.get('1.0','end-1c'));self.editor.edit_modified(False)
  except OSError:messagebox.showerror('保存失败','无法保存配置文件，请检查权限。');return False
  if show:self.status.set('配置已保存：'+self.filevar.get())
  return True
 def launch(self,action):
  if self.busy or not self.save_file():return
  chosen=[k for k,v in self.selected.items() if v.get()]
  if not chosen:messagebox.showinfo('选择组件','请至少勾选一个组件。');return
  if Path(self.profile.get())!=self.loaded_profile:
   messagebox.showinfo('配置目录','手动修改路径后，请使用“选择目录”加载对应配置。');return
  self.busy=True
  for button in [self.buildbtn,self.checkbtn,self.newbtn,self.savebtn]:button.configure(state='disabled')
  self.editor.configure(state='disabled');self.combo.configure(state='disabled');self.progress.start(15);self.notebook.select(2);self.status.set('正在校验…' if action=='check' else '正在构建所选组件…')
  args=[sys.executable,str(package.ROOT/'tools/package.py'),action,'--profile',self.profile.get(),'--output',self.output.get(),'--version',self.version.get(),'--components',','.join(chosen)]
  def work():
   env=os.environ.copy();env['PYTHONIOENCODING']='utf-8';env['PYTHONUNBUFFERED']='1'
   try:
    process=subprocess.Popen(args,cwd=package.ROOT,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,encoding='utf-8',errors='replace',env=env)
    for line in process.stdout:self.events.put(('log',line))
    self.events.put(('done',(process.wait(),action)))
   except Exception:self.events.put(('log','无法启动构建进程，请检查 Python/Go/Node 安装。\n'));self.events.put(('done',(1,action)))
  threading.Thread(target=work,daemon=True).start()
 def poll(self):
  try:
   while True:
    kind,value=self.events.get_nowait()
    if kind=='log':self.log.configure(state='normal');self.log.insert('end',value);self.log.see('end');self.log.configure(state='disabled')
    else:
     code,action=value;self.busy=False;self.progress.stop()
     for button in [self.buildbtn,self.checkbtn,self.newbtn,self.savebtn]:button.configure(state='normal')
     self.editor.configure(state='normal');self.combo.configure(state='readonly')
     self.status.set(('校验通过' if action=='check' else '打包完成：'+self.output.get()) if code==0 else '未完成：请查看日志并修改配置后重试。')
  except queue.Empty:pass
  self.root.after(100,self.poll)

def main():
 root=tk.Tk();app=App(root)
 def closing():
  if app.busy:messagebox.showinfo('正在打包','请等待当前构建结束后再关闭窗口。');return
  if app.save_file():root.destroy()
 root.protocol('WM_DELETE_WINDOW',closing);root.mainloop()
if __name__=='__main__':main()
