In my school, people want to play games and do stuff the school has unfairly prohibited.
Currently, nearly the entire school utilises xVPN with it's hard to block Everest protocol. 
My product utilises social engineering and middlemen (who claim a portion of their clients payments) across different houses to facilitate clients. This is better because students who have cash, but unable to pay for an online plan with a credit card (because that requires a parent's card).
On another note, the WIFI is incredibly congested, meaning that the xVPN free plan sucks for gaming and there is demand for other services.
The amount of clients to turn a profit it low, so I'm generous with the plans, including a small gap between standard and premium to "accentuate the deal":

| Lite/Basic Plan | Standard Plan | Premium Plan |
| --------------- | ------------- | ------------ |
| $5              | $9            | $10          |

Lite/Basic Plan:

This Plan will have to use a protocol that uses the least amount of system resources, presents the least risk to my VPS IP, and isn't good enough for gaming so people will upgrade, but amazing for web browsing alone. It will have to be optimised so it's fast enough to work, but not enough to convince people to stay.



Standard Plan:
This should be self explanatory. Should feel extremely lacking compared with the Premium plan, however can play "some" games and still perform ok. This option is more to convince you to move directly onto the premium plan utilising the decoy effect.

Premium Plan:
Everything. Good gaming, good streaming, good nearly everything except high volume profesh work that's out of my scope.

Protocols:
So far, the top contenders:
TUIC V5
Hysteria 2
AmneziaWG
Vless-Reality (Extreme cases)

I have yet to test these on the networks in question, which I will then decide which ones are implemented. But each plan will introduce different bottlenecks and safety (for me, not the clients) features. Since they're operating on congested WIFIs (Schools don't exactly provide)
E.g:
Hysteria 2: Obfuscation which tends to add overhead
AmneziaWG: A bunch of junk that causes overhead
VLESS-REALITY: Already tons of protection for my VPS, however for the Lite plan I'll leave it on standard TCP.
TUIC V5: Set the congestion control congestion algorithm to `cubic` (instead of `bbr`) and **disable UDP packet relay** (`"udp_relay": false`)

# How will they connect?

So basically I'll grab something like hiddify-app, get AI to quickly debrand it, adjust some elements to better fit my services, and set-up an one-time activation code system which ties to a centralised architecture where I can control subscriptions and etc.
