#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <stdio.h>
#include "../src/window/window.m"

static BOOL expectConfig;
static int readyCount;

void WindowDidClose(void) {}

// Simulate Go flushing its startup queue when the native bridge reports readiness.
void WindowIsReady(void) {
    if (++readyCount != 1) {
        fprintf(stderr, "UI reported readiness more than once\n");
        exit(1);
    }
    WKWebView *view = [windowController valueForKey:@"webView"];
    [view evaluateJavaScript:@"typeof window.finicky.receiveMessage" completionHandler:^(id value, NSError *error) {
        if (error || ![value isEqual:@"function"]) {
            fprintf(stderr, "Native queue flushed before the UI receiver existed\n");
            exit(1);
        }
    }];
    if (expectConfig) {
        SendMessageToWebView("{\"type\":\"config\",\"message\":{\"configPath\":\"/test/finicky.ts\"}}");
    }
    SendMessageToWebView("{\"type\":\"version\",\"message\":\"4.2.2-test\"}");
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC / 4), dispatch_get_main_queue(), ^{
        NSString *script = @"({text:document.body.innerText,bridge:typeof window.finicky.receiveMessage})";
        [view evaluateJavaScript:script completionHandler:^(id value, NSError *error) {
            NSString *text = [value isKindOfClass:[NSDictionary class]] ? value[@"text"] : nil;
            BOOL loaded = [text containsString:@"Loaded Configuration"];
            BOOL missing = [text containsString:@"No Configuration Found"];
            if (error || !text || loaded != expectConfig || missing == expectConfig ||
                ![text containsString:@"4.2.2-test"] ||
                (expectConfig && ![text containsString:@"/test/finicky.ts"])) {
                fprintf(stderr, "Incorrect UI state after startup messages: %s; error=%s\n",
                        [[value description] UTF8String], [[error description] UTF8String]);
                exit(1);
            }
            printf("PASS: startup messages delivered after receiver installation (%s config)\n",
                   expectConfig ? "loaded" : "missing");
            exit(0);
        }];
    });
}

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        if (argc != 3) {
            fprintf(stderr, "Usage: window-ready UI_DIST_DIRECTORY normal|delayed|missing\n");
            return 2;
        }
        NSString *scenario = [NSString stringWithUTF8String:argv[2]];
        expectConfig = ![scenario isEqual:@"missing"];
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
        NSString *root = [NSString stringWithUTF8String:argv[1]];
        NSString *html = [NSString stringWithContentsOfFile:[root stringByAppendingPathComponent:@"index.html"]
                                                 encoding:NSUTF8StringEncoding error:nil];
        if (!html) {
            fprintf(stderr, "Build the UI before running this test\n");
            return 2;
        }
        if ([scenario isEqual:@"delayed"]) {
            // Finish navigation before the UI module even starts loading.
            NSString *original = @"<script type=\"module\" crossorigin src=\"/index.js\"></script>";
            if (![html containsString:original]) {
                fprintf(stderr, "UI entry script changed; update the delayed-start fixture\n");
                return 2;
            }
            html = [html stringByReplacingOccurrencesOfString:original
                                                  withString:@"<script type=\"module\">setTimeout(() => import('/index.js'), 250);</script>"];
        }
        SetHTMLContent([html UTF8String]);
        for (NSString *file in @[@"index.js", @"index.css"]) {
            NSString *content = [NSString stringWithContentsOfFile:[root stringByAppendingPathComponent:file]
                                                          encoding:NSUTF8StringEncoding error:nil];
            if (!content) {
                fprintf(stderr, "Missing built UI asset: %s\n", [file UTF8String]);
                return 2;
            }
            SetFileContent([file UTF8String], [content UTF8String]);
        }
        windowController = [[WindowController alloc] init];
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
            fprintf(stderr, "Timed out waiting for UI readiness\n");
            exit(1);
        });
        [NSApp run];
    }
}
