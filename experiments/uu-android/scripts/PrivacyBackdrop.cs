using System;using System.Windows.Forms;using System.Drawing;using System.Threading;using System.Runtime.InteropServices;using System.Text;
class PrivacyBackdrop {
 [DllImport("user32.dll")] static extern bool ShowWindow(IntPtr h,int c);
 [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] static extern IntPtr GetThreadDesktop(uint id);
 [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern bool GetUserObjectInformation(IntPtr h,int n,StringBuilder v,int length,out int needed);
 [DllImport("user32.dll")] static extern bool SetProcessDpiAwarenessContext(IntPtr context);
 [STAThread] static void Main(string[] args){
 SetProcessDpiAwarenessContext(new IntPtr(-4));Application.EnableVisualStyles();Form f=new Form();
 f.Text="DD privacy capture test";f.FormBorderStyle=FormBorderStyle.None;f.StartPosition=FormStartPosition.Manual;f.Bounds=SystemInformation.VirtualScreen;f.TopMost=true;f.ShowInTaskbar=true;
 f.Paint+=(s,e)=>{int w=f.ClientSize.Width,h=f.ClientSize.Height;e.Graphics.FillRectangle(Brushes.Red,0,0,w/2,h/2);e.Graphics.FillRectangle(Brushes.Lime,w/2,0,w/2,h/2);e.Graphics.FillRectangle(Brushes.Blue,0,h/2,w/2,h/2);e.Graphics.FillRectangle(Brushes.White,w/2,h/2,w/2,h/2);};
 var show=new System.Windows.Forms.Timer();show.Interval=500;show.Tick+=(s,e)=>{
 show.Stop();ShowWindow(f.Handle,5);ShowWindow(f.Handle,5);f.Show();f.Refresh();
 var name=new StringBuilder(256);int needed;GetUserObjectInformation(GetThreadDesktop(GetCurrentThreadId()),2,name,512,out needed);
 Console.WriteLine("READY hwnd="+f.Handle+" visible="+IsWindowVisible(f.Handle)+" desktop="+name+" bounds="+f.Bounds);Console.Out.Flush();};show.Start();
 new Thread(()=>{Console.ReadLine();try{f.BeginInvoke(new Action(()=>f.Close()));}catch{}}){IsBackground=true}.Start();
 if(Array.IndexOf(args,"--animate")>=0){int frame=0;var animate=new System.Windows.Forms.Timer();animate.Interval=33;animate.Tick+=(s,e)=>{frame++;f.Invalidate();};f.Paint+=(s,e)=>{e.Graphics.FillRectangle(Brushes.Black,(frame*8)%Math.Max(1,f.ClientSize.Width-80),f.ClientSize.Height/2-20,80,40);};animate.Start();}
 var timer=new System.Windows.Forms.Timer();timer.Interval=90000;timer.Tick+=(s,e)=>f.Close();timer.Start();Application.Run(f);
 }
}
