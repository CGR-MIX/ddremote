using System;using System.IO;using System.Windows.Forms;using System.Drawing;using System.Threading;using System.Runtime.InteropServices;
public static class InputTarget {
 [DllImport("user32.dll")] static extern bool ShowWindow(IntPtr h,int c);
 [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] static extern bool SetProcessDpiAwarenessContext(IntPtr context);
 static bool first,last,drag;static int left,right,wheels;static Point start;static Form form;
 static string report;
 static void Save(){File.WriteAllText(report,"{\"firstChineseEmoji\":"+first.ToString().ToLower()+",\"shortcutReplace\":"+last.ToString().ToLower()+",\"leftClicks\":"+left+",\"rightClicks\":"+right+",\"wheelEvents\":"+wheels+",\"dragged\":"+drag.ToString().ToLower()+",\"visible\":"+IsWindowVisible(form.Handle).ToString().ToLower()+",\"foreground\":"+(GetForegroundWindow()==form.Handle).ToString().ToLower()+"}");}
 [STAThread] public static void Main(string[] args){
 report=args.Length>0?args[0]:@"D:\DSHworkspace\_toolchain\ddremote-uu-android\input-result.json";
 SetProcessDpiAwarenessContext(new IntPtr(-4));Application.EnableVisualStyles();Form f=new Form();form=f;f.Text="DD UU synthetic input test";f.FormBorderStyle=FormBorderStyle.None;f.StartPosition=FormStartPosition.Manual;f.Bounds=SystemInformation.VirtualScreen;f.TopMost=true;
 TextBox box=new TextBox();box.Multiline=true;box.Dock=DockStyle.Fill;box.Font=new Font("Microsoft YaHei UI",22);f.Controls.Add(box);
 box.TextChanged+=(s,e)=>{if(box.Text=="你好🙂 DD")first=true;if(box.Text=="DD 输入验证🙂")last=true;Save();};
 box.MouseDown+=(s,e)=>{start=e.Location;if(e.Button==MouseButtons.Left)left++;if(e.Button==MouseButtons.Right)right++;Save();};
 box.MouseMove+=(s,e)=>{if(e.Button==MouseButtons.Left&&Math.Abs(e.X-start.X)>20){drag=true;Save();}};
 box.MouseWheel+=(s,e)=>{wheels++;Save();};
 var show=new System.Windows.Forms.Timer();show.Interval=500;show.Tick+=(s,e)=>{show.Stop();ShowWindow(f.Handle,5);ShowWindow(f.Handle,5);f.Show();f.Activate();box.Focus();Save();Console.WriteLine("READY visible="+IsWindowVisible(f.Handle));Console.Out.Flush();};show.Start();
 new Thread(()=>{Console.ReadLine();try{f.BeginInvoke(new Action(()=>f.Close()));}catch{}}){IsBackground=true}.Start();
 var observe=new System.Windows.Forms.Timer();observe.Interval=200;observe.Tick+=(s,e)=>Save();observe.Start();
 var timer=new System.Windows.Forms.Timer();timer.Interval=90000;timer.Tick+=(s,e)=>f.Close();timer.Start();Application.Run(f);
 }
}
