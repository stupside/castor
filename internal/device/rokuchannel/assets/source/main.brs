sub Main(args as Dynamic)
    screen = CreateObject("roSGScreen")
    port = CreateObject("roMessagePort")
    screen.setMessagePort(port)

    scene = screen.CreateScene("MainScene")
    screen.show()
    scene.setField("args", args)

    input = CreateObject("roInput")
    input.setMessagePort(port)

    while true
        msg = wait(0, port)
        if type(msg) = "roSGScreenEvent"
            if msg.isScreenClosed() then return
        else if type(msg) = "roInputEvent"
            if msg.isInput() then scene.setField("args", msg.getInfo())
        end if
    end while
end sub
