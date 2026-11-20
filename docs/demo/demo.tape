# Records the README screenshots. Each scene is a Neovim process that
# waits until the language server has drawn the feature; the Wait
# patterns are text the server adds, not text already in the file.
# Run from the repository root via docs/demo/render.sh.

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Columns 124
Set Rows 40
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 1ms
Set Framerate 8
Set CursorBlink false
Set WaitTimeout 30s

Hide
Type "export PS1='$ '"
Enter

Type "./docs/demo/scene.sh hover"
Enter
Wait+Screen@30s /UNSIGNED\(7 downto 0\)/
Show
Sleep 600ms
Screenshot docs/images/hover.png
Sleep 400ms

Hide
Escape
Escape
Type ":qa!"
Enter
Wait+Screen@15s /\$ /

Type "./docs/demo/scene.sh completion"
Enter
Wait+Screen@30s /STD_LOGIC/
Show
Sleep 600ms
Screenshot docs/images/completion.png
Sleep 400ms

Hide
Escape
Escape
Type ":qa!"
Enter
Wait+Screen@15s /\$ /

Type "./docs/demo/scene.sh definition"
Enter
Wait+Screen@30s /Width-generic/
Show
Sleep 600ms
Screenshot docs/images/definition.png
Sleep 400ms

Hide
Escape
Escape
Type ":qa!"
Enter
Wait+Screen@15s /\$ /

Type "./docs/demo/scene.sh references"
Enter
Wait+Screen@30s / col /
Show
Sleep 600ms
Screenshot docs/images/references.png
Sleep 400ms

Hide
Escape
Escape
Type ":qa!"
Enter
Wait+Screen@15s /\$ /

Type "./docs/demo/scene.sh signature"
Enter
Wait+Screen@30s /from work.rtl/
Show
Sleep 600ms
Screenshot docs/images/signature.png
Sleep 400ms

Hide
Escape
Escape
Type ":qa!"
Enter
Wait+Screen@15s /\$ /

Type "./docs/demo/scene.sh diagnostics"
Enter
Wait+Screen@30s /not declared/
Show
Sleep 600ms
Screenshot docs/images/diagnostics.png
Sleep 400ms

Hide
Escape
Escape
Type ":qa!"
Enter
Wait+Screen@15s /\$ /

Type "./docs/demo/scene.sh symbols"
Enter
Wait+Screen@30s /Method/
Show
Sleep 600ms
Screenshot docs/images/symbols.png
Sleep 400ms
