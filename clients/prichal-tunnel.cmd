@echo off
chcp 65001 >nul
rem ===== Причал: SSH-туннель к панели (Windows) =====
rem Поменяйте SERVER на адрес своего сервера. EXTRA - другие порты сервера,
rem которые тоже нужно открыть через туннель (например, 5678 для n8n).
set "SERVER=root@АДРЕС-СЕРВЕРА"
set "PORT=9443"
set "EXTRA="

title Причал - SSH-туннель (не закрывайте это окно)
set "SSH=%SystemRoot%\System32\OpenSSH\ssh.exe"
set "URL=http://localhost:%PORT%"
set "FWD=-L %PORT%:127.0.0.1:%PORT%"
for %%p in (%EXTRA%) do call set "FWD=%%FWD%% -L %%p:127.0.0.1:%%p"

rem Туннель уже открыт в другом окне: просто открываем браузер.
powershell -NoProfile -Command "$c=New-Object Net.Sockets.TcpClient; try{ if($c.ConnectAsync('127.0.0.1',%PORT%).Wait(700)){exit 0} }catch{}; exit 1"
if %errorlevel%==0 (
  start "" "%URL%"
  exit /b
)

rem В фоне ждём, пока туннель заработает, и открываем браузер.
start "" /min powershell -NoProfile -WindowStyle Hidden -Command "for($i=0;$i -lt 300;$i++){ $c=New-Object Net.Sockets.TcpClient; try{ if($c.ConnectAsync('127.0.0.1',%PORT%).Wait(500)){ Start-Process '%URL%'; break } }catch{} finally{ $c.Dispose() }; Start-Sleep -Milliseconds 700 }"

echo.
echo  Подключение к %SERVER%
echo  1. Введите пароль (или подтвердите ключ) и нажмите Enter.
echo     Символы пароля при вводе не видны - так и задумано.
echo  2. Браузер сам откроет Причал.
echo  3. Пока работаете, не закрывайте это окно.
echo.

"%SSH%" -N %FWD% -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 %SERVER%

echo.
echo Соединение закрыто.
pause
